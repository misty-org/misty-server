package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Bounds for work a member asks of another member's agent. The work uses the
// owner's agent on someone else's behalf, so a request gets fewer model turns
// and less time than a run the owner starts themselves, and an agent takes a
// few requests at a time.
const (
	memberRequestModelTurns    = 12
	memberRequestExecutionMS   = 10 * 60 * 1000
	memberRequestsPerRun       = 5
	memberRequestsPerUserHour  = 60
	memberRequestMaxDepth      = 2
	memberRequestMaxOpen       = 3
	memberRequestActiveRunList = `'queued','running','awaiting_device','awaiting_intervention','cooldown','retrying'`
)

// ExternalAgentRequesterPrefix marks requests one of the member's agents sent
// through the A2A endpoint rather than from an agent run. They count only
// toward the hourly limit.
const ExternalAgentRequesterPrefix = "a2a:"

func ExternalAgentRequester(userID, agentID string) string {
	return ExternalAgentRequesterPrefix + userID + ":" + agentID
}

// MentionRequesterPrefix marks requests a person made by @mentioning an agent
// in Space chat. The message stands in for the requesting run.
const MentionRequesterPrefix = "message:"

func MentionRequester(messageID string) string { return MentionRequesterPrefix + messageID }

// ReplyRequesterPrefix marks requests an agent made by @mentioning another
// agent in its chat reply. The reply stands in for the requesting run, the
// work is billed to the person who asked first, and it goes one hop deep.
const ReplyRequesterPrefix = "reply:"

func ReplyRequester(messageID string) string { return ReplyRequesterPrefix + messageID }

type AgentMemberRequestInput struct {
	SpaceID          string
	RequesterUserID  string
	RequesterAgentID string
	RequesterRunID   string
	TargetAgentID    string
	Message          string
	IdempotencyKey   string
	Timezone         string
	// SourceConversationID is the chat the mentioning message was sent in;
	// empty means the Space's main chat.
	SourceConversationID string
}

// AgentMemberRequest joins the request to the current state of its run.
type AgentMemberRequest struct {
	ID                string          `json:"id"`
	SpaceID           string          `json:"space_id"`
	SpaceName         string          `json:"space_name"`
	RequesterUserID   string          `json:"requester_user_id"`
	RequesterName     string          `json:"requester_name"`
	RequesterAgentID  string          `json:"requester_agent_id"`
	RequesterRunID    string          `json:"requester_run_id"`
	TargetAgentID     string          `json:"target_agent_id"`
	TargetAgentName   string          `json:"target_agent_name"`
	TargetOwnerUserID string          `json:"target_owner_user_id"`
	TargetOwnerName   string          `json:"target_owner_name"`
	ChildRunID        string          `json:"child_run_id"`
	Message           string          `json:"message"`
	CreatedAt         time.Time       `json:"created_at"`
	RunState          string          `json:"run_state"`
	RunResult         json.RawMessage `json:"run_result"`
	RunErrorMessage   string          `json:"run_error_message"`
	// SourceMessageID is set when a person @mentioned the agent in Space chat;
	// the reply posts under that message.
	SourceMessageID      string `json:"source_message_id,omitempty"`
	SourceConversationID string `json:"source_conversation_id,omitempty"`
	Replay               bool   `json:"-"`
}

const agentMemberRequestSelect = `SELECT q.id,q.space_id,s.name,q.requester_user_id,COALESCE(ru.username,''),q.requester_agent_id,q.requester_run_id,
	q.target_agent_id,COALESCE(a.name,''),q.target_owner_user_id,COALESCE(ou.username,''),q.child_run_id,q.message,q.created_at,
	r.state,r.result,COALESCE(r.error_message,''),COALESCE(q.source_message_id,''),COALESCE(q.source_conversation_id,'')
	FROM agent_member_requests q JOIN spaces s ON s.id=q.space_id JOIN space_runs r ON r.id=q.child_run_id
	LEFT JOIN misty_ask_identities a ON a.id=q.target_agent_id
	LEFT JOIN users ru ON ru.id=q.requester_user_id LEFT JOIN users ou ON ou.id=q.target_owner_user_id `

func scanAgentMemberRequest(row interface{ Scan(...any) error }, out *AgentMemberRequest) error {
	return row.Scan(&out.ID, &out.SpaceID, &out.SpaceName, &out.RequesterUserID, &out.RequesterName, &out.RequesterAgentID, &out.RequesterRunID,
		&out.TargetAgentID, &out.TargetAgentName, &out.TargetOwnerUserID, &out.TargetOwnerName, &out.ChildRunID, &out.Message, &out.CreatedAt,
		&out.RunState, &out.RunResult, &out.RunErrorMessage, &out.SourceMessageID, &out.SourceConversationID)
}

// CreateAgentMemberRequest sends work to an agent in a Space, from another
// agent's run or from a person @mentioning it in Space chat. The delegated run
// acts as the target agent, only in that Space, and is billed to the person
// who asked. It starts at once. A repeated idempotency key from the same run
// (or message) returns the original request.
func (db *Database) CreateAgentMemberRequest(ctx context.Context, input AgentMemberRequestInput) (*AgentMemberRequest, error) {
	input.Message = strings.TrimSpace(input.Message)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.Timezone = strings.TrimSpace(input.Timezone)
	if input.Timezone == "" {
		input.Timezone = "UTC"
	}
	if input.SpaceID == "" || input.RequesterUserID == "" || input.RequesterRunID == "" || input.TargetAgentID == "" ||
		input.Message == "" || len([]rune(input.Message)) > 16_000 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 200 {
		return nil, ErrSpaceInvalid
	}
	if _, err := time.LoadLocation(input.Timezone); err != nil {
		return nil, ErrSpaceInvalid
	}
	out := &AgentMemberRequest{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT id FROM agent_member_requests WHERE requester_run_id=$1 AND idempotency_key=$2`, input.RequesterRunID, input.IdempotencyKey).Scan(&existing)
		if err == nil {
			out.Replay = true
			return scanAgentMemberRequest(tx.QueryRowContext(ctx, agentMemberRequestSelect+`WHERE q.id=$1 AND q.requester_user_id=$2`, existing, input.RequesterUserID), out)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := requireSpaceMemberTx(ctx, tx, input.SpaceID, input.RequesterUserID); err != nil {
			return err
		}
		if allowed, err := hasSpacePermissionTx(ctx, tx, input.RequesterUserID, input.SpaceID, PermissionAskRun); err != nil || !allowed {
			if err == nil {
				err = ErrSpaceForbidden
			}
			return err
		}
		parentRunID, parentInvocationID, depth, err := memberRequestParentTx(ctx, tx, input.RequesterUserID, input.RequesterAgentID, input.RequesterRunID)
		if err != nil {
			return err
		}
		sourceMessageID := ""
		if strings.HasPrefix(input.RequesterRunID, MentionRequesterPrefix) {
			sourceMessageID = strings.TrimPrefix(input.RequesterRunID, MentionRequesterPrefix)
			if err := requireMentionMessageTx(ctx, tx, input, sourceMessageID); err != nil {
				return err
			}
		}
		if strings.HasPrefix(input.RequesterRunID, ReplyRequesterPrefix) {
			sourceMessageID = strings.TrimPrefix(input.RequesterRunID, ReplyRequesterPrefix)
		}
		target, err := memberRequestTargetTx(ctx, tx, input)
		if err != nil {
			return err
		}
		if err := memberRequestLimitsTx(ctx, tx, input); err != nil {
			return err
		}
		mode := target.defaultMode
		if runModeRank(mode) > runModeRank("auto") {
			mode = "auto"
		}
		snapshot := mustJSON(map[string]any{"id": input.TargetAgentID, "version": target.version, "version_id": target.versionID, "name": target.name,
			"instructions": target.instructions, "model_id": target.modelID, "reasoning_effort": target.effort, "default_run_mode": target.defaultMode, "system_managed": false})
		requestID := "agentreq_" + uuid.NewString()
		runInput := mustJSON(map[string]any{
			"instruction": input.Message, "timezone": input.Timezone, "member_request_id": requestID,
			"requester_user_id": input.RequesterUserID, "requester_invocation_id": parentInvocationID,
		})
		runID := "run_" + uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_runs(
			id,space_id,resource_kind,resource_id,initiated_by_user_id,billing_user_id,trigger_kind,state,input,result,
			requesting_member_id,source_type,agent_id,outputs,artifacts,attempt,conversation_scope_kind,owner_user_id,
			initial_run_mode,effective_run_mode,agent_version_snapshot,parent_run_id,delegation_depth,context_bindings,
			model_turn_limit,execution_limit_ms)
			VALUES($1,$2,'agent',$3,$4,$4,'delegated','queued',$5,'{}'::jsonb,$6,'direct',$3,'{}'::jsonb,'[]'::jsonb,1,'everyone',$6,
			$7,$7,$8,NULLIF($9,''),$10,'[]'::jsonb,$11,$12)`,
			runID, input.SpaceID, input.TargetAgentID, input.RequesterUserID, runInput, target.ownerUserID,
			mode, snapshot, parentRunID, depth, memberRequestModelTurns, memberRequestExecutionMS); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_member_requests(id,space_id,requester_user_id,requester_agent_id,requester_run_id,
			target_agent_id,target_owner_user_id,child_run_id,message,idempotency_key,source_message_id,source_conversation_id)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),NULLIF($12,''))`,
			requestID, input.SpaceID, input.RequesterUserID, input.RequesterAgentID, input.RequesterRunID, input.TargetAgentID,
			target.ownerUserID, runID, input.Message, input.IdempotencyKey, sourceMessageID, input.SourceConversationID); err != nil {
			return err
		}
		if err := queueMemberRequestRunTx(ctx, tx, runID, input.SpaceID, input.TargetAgentID); err != nil {
			return err
		}
		if _, err := recordSpaceEventTx(ctx, tx, input.SpaceID, input.RequesterUserID, "agent.request.created", requestID, map[string]any{
			"request_id": requestID, "requester_agent_id": input.RequesterAgentID, "target_agent_id": input.TargetAgentID, "target_agent_name": target.name,
			"target_owner_user_id": target.ownerUserID,
		}); err != nil {
			return err
		}
		return scanAgentMemberRequest(tx.QueryRowContext(ctx, agentMemberRequestSelect+`WHERE q.id=$1`, requestID), out)
	})
	return out, err
}

// memberRequestParentTx proves the requester owns the live run asking for
// help and bounds how deep delegated work can go.
func memberRequestParentTx(ctx context.Context, tx *sql.Tx, userID, agentID, runID string) (parentRunID, parentInvocationID string, depth int, err error) {
	if strings.HasPrefix(runID, MentionRequesterPrefix) {
		// A person asked in Space chat; requireMentionMessageTx checks the message.
		if agentID != "" {
			return "", "", 0, ErrSpaceForbidden
		}
		return "", "", 1, nil
	}
	if strings.HasPrefix(runID, ReplyRequesterPrefix) {
		// An agent's chat reply, answering a request this person made, mentioned
		// another agent. It continues that request's work one level deeper.
		var parentAgentID string
		var parentDepth int
		err = tx.QueryRowContext(ctx, `SELECT q.child_run_id,q.target_agent_id,r.delegation_depth FROM space_messages m
			JOIN agent_member_requests q ON q.id=m.origin->>'agent_request_id'
			JOIN space_runs r ON r.id=q.child_run_id
			WHERE m.id=$1 AND m.sender_kind='agent' AND m.sender_agent_id=q.target_agent_id AND q.requester_user_id=$2`,
			strings.TrimPrefix(runID, ReplyRequesterPrefix), userID).Scan(&parentRunID, &parentAgentID, &parentDepth)
		if err != nil || agentID == "" || agentID != parentAgentID {
			return "", "", 0, ErrSpaceForbidden
		}
		if parentDepth+1 > memberRequestMaxDepth {
			return "", "", 0, ErrAgentRequestLimit
		}
		return parentRunID, "", parentDepth + 1, nil
	}
	if strings.HasPrefix(runID, ExternalAgentRequesterPrefix) {
		// An A2A caller is one of the signed-in member's own agents, outside
		// any Misty run. The endpoint verified its agent token; this re-checks
		// ownership in the same transaction that creates the request.
		if agentID == "" || runID != ExternalAgentRequester(userID, agentID) {
			return "", "", 0, ErrSpaceForbidden
		}
		if owned, err := requesterAgentActiveTx(ctx, tx, userID, agentID); err != nil || !owned {
			if err == nil {
				err = ErrSpaceForbidden
			}
			return "", "", 0, err
		}
		return "", "", 1, nil
	}
	var chained bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_member_requests WHERE child_run_id=$1)`, runID).Scan(&chained); err != nil {
		return "", "", 0, err
	}
	if chained {
		return "", "", 0, ErrAgentRequestChained
	}
	if invocationRunIdentity(runID) {
		var owner, state string
		if err = tx.QueryRowContext(ctx, `SELECT user_id,state FROM ai_invocations WHERE id=$1 FOR UPDATE`, runID).Scan(&owner, &state); err != nil || owner != userID || state != "running" {
			return "", "", 0, ErrSpaceForbidden
		}
		return "", runID, 1, nil
	}
	var owner, state string
	var parentDepth int
	if err = tx.QueryRowContext(ctx, `SELECT owner_user_id,state,delegation_depth FROM space_runs WHERE id=$1 FOR UPDATE`, runID).Scan(&owner, &state, &parentDepth); err != nil || owner != userID {
		return "", "", 0, ErrSpaceForbidden
	}
	if state != "running" && state != "awaiting_device" {
		return "", "", 0, ErrSpaceForbidden
	}
	if parentDepth+1 > memberRequestMaxDepth {
		return "", "", 0, ErrAgentRequestLimit
	}
	return runID, "", parentDepth + 1, nil
}

type memberRequestTarget struct {
	ownerUserID, name, instructions, modelID, effort, defaultMode, versionID string
	version                                                                  int64
}

// memberRequestTargetTx finds the agent asked. A private agent is refused to
// everyone but its owner, the same way as an agent not in the Space.
func memberRequestTargetTx(ctx context.Context, tx *sql.Tx, input AgentMemberRequestInput) (memberRequestTarget, error) {
	var target memberRequestTarget
	err := tx.QueryRowContext(ctx, `SELECT m.owner_user_id,a.name,a.instructions,a.model_id,a.reasoning_effort,a.default_run_mode,a.version,v.id`+
		spaceAgentJoins+` JOIN misty_ask_identity_versions v ON v.agent_id=a.id AND v.version=a.version
		WHERE m.space_id=$1 AND m.agent_id=$2 AND (m.visibility='public' OR m.owner_user_id=$3) FOR SHARE OF m`, input.SpaceID, input.TargetAgentID, input.RequesterUserID).
		Scan(&target.ownerUserID, &target.name, &target.instructions, &target.modelID, &target.effort, &target.defaultMode, &target.version, &target.versionID)
	if errors.Is(err, sql.ErrNoRows) {
		return target, ErrAgentNotInSpace
	}
	if err != nil {
		return target, err
	}
	if input.RequesterAgentID != "" && input.RequesterAgentID == input.TargetAgentID {
		return target, ErrAgentNotInSpace
	}
	if !validAgentRunMode(target.defaultMode) {
		target.defaultMode = "auto"
	}
	return target, nil
}

func memberRequestLimitsTx(ctx context.Context, tx *sql.Tx, input AgentMemberRequestInput) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "agent-member-request:"+input.TargetAgentID); err != nil {
		return err
	}
	var open, perRun, lastHour int
	if err := tx.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM agent_member_requests q JOIN space_runs r ON r.id=q.child_run_id WHERE q.target_agent_id=$1 AND r.state IN (`+memberRequestActiveRunList+`)),
		(SELECT COUNT(*) FROM agent_member_requests WHERE requester_run_id=$2),
		(SELECT COUNT(*) FROM agent_member_requests WHERE requester_user_id=$3 AND created_at>NOW()-INTERVAL '1 hour')`,
		input.TargetAgentID, input.RequesterRunID, input.RequesterUserID).Scan(&open, &perRun, &lastHour); err != nil {
		return err
	}
	if open >= memberRequestMaxOpen {
		return ErrAgentRequestBusy
	}
	external := strings.HasPrefix(input.RequesterRunID, ExternalAgentRequesterPrefix) || strings.HasPrefix(input.RequesterRunID, MentionRequesterPrefix) ||
		strings.HasPrefix(input.RequesterRunID, ReplyRequesterPrefix)
	if (!external && perRun >= memberRequestsPerRun) || lastHour >= memberRequestsPerUserHour {
		return ErrAgentRequestLimit
	}
	return nil
}

// AgentMemberRequestForUser returns a request to either party.
func (db *Database) AgentMemberRequestForUser(ctx context.Context, userID, requestID string) (*AgentMemberRequest, error) {
	out := &AgentMemberRequest{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return scanAgentMemberRequest(tx.QueryRowContext(ctx, agentMemberRequestSelect+`WHERE q.id=$1 AND (q.requester_user_id=$2 OR q.target_owner_user_id=$2)`, requestID, userID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

// AgentMentionsForUser lists the agent runs the person started by @mentioning
// an agent in Space chat, newest first. Only the person who mentioned sees them;
// everyone else in the chat sees just the agent's reply.
func (db *Database) AgentMentionsForUser(ctx context.Context, userID string, limit int) ([]AgentMemberRequest, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	out := []AgentMemberRequest{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, agentMemberRequestSelect+`WHERE q.requester_user_id=$1 AND q.requester_run_id LIKE $2
			ORDER BY q.created_at DESC LIMIT $3`, userID, MentionRequesterPrefix+"%", limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentMemberRequest
			if err := scanAgentMemberRequest(rows, &item); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}

// AgentRequestStep is one step of an agent's run, as the person who asked sees
// it: what it was, how it ended, and when. Inputs and outputs stay out; they can
// hold the owner's private data.
type AgentRequestStep struct {
	Name        string     `json:"name"`
	State       string     `json:"state"`
	Error       string     `json:"error,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// AgentRequestTranscript returns the steps of the run behind a request to the
// person who made it, and to nobody else (not even the agent's owner).
func (db *Database) AgentRequestTranscript(ctx context.Context, userID, requestID string) ([]AgentRequestStep, error) {
	steps := []AgentRequestStep{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var runID string
		if err := tx.QueryRowContext(ctx, `SELECT child_run_id FROM agent_member_requests WHERE id=$1 AND requester_user_id=$2`, requestID, userID).Scan(&runID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT node_id,state,COALESCE(error_message,''),started_at,completed_at FROM space_run_steps WHERE run_id=$1 ORDER BY COALESCE(started_at,updated_at),node_id`, runID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var step AgentRequestStep
			if err := rows.Scan(&step.Name, &step.State, &step.Error, &step.StartedAt, &step.CompletedAt); err != nil {
				return err
			}
			steps = append(steps, step)
		}
		return rows.Err()
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return steps, err
}

// AgentMemberRequestForRun finds the request a delegated run is doing.
// ErrSpaceNotFound means the run is not a member request.
func (db *Database) AgentMemberRequestForRun(ctx context.Context, runID string) (*AgentMemberRequest, error) {
	out := &AgentMemberRequest{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return scanAgentMemberRequest(tx.QueryRowContext(ctx, agentMemberRequestSelect+`WHERE q.child_run_id=$1`, runID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

// requireMentionMessageTx proves the requester sent the chat message that
// @mentions the agent, in the Space and chat the request names. The owner must
// be able to post the reply there.
func requireMentionMessageTx(ctx context.Context, tx *sql.Tx, input AgentMemberRequestInput, messageID string) error {
	var mentioned bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_messages m
		WHERE m.id=$1 AND m.space_id=$2 AND m.sender_user_id=$3 AND m.sender_kind='person' AND COALESCE(m.conversation_id,'')=$4
		  AND m.content @> jsonb_build_array(jsonb_build_object('type','mention','agent_id',$5::text)))`,
		messageID, input.SpaceID, input.RequesterUserID, input.SourceConversationID, input.TargetAgentID).Scan(&mentioned); err != nil {
		return err
	}
	if !mentioned {
		return ErrSpaceForbidden
	}
	if input.SourceConversationID == "" {
		return nil
	}
	var ownerInChat bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_conversation_members cm
		JOIN misty_ask_identities a ON a.owner_user_id=cm.user_id
		WHERE cm.conversation_id=$1 AND a.id=$2)`, input.SourceConversationID, input.TargetAgentID).Scan(&ownerInChat); err != nil {
		return err
	}
	if !ownerInChat {
		return ErrAgentNotInSpace
	}
	return nil
}
