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
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
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
