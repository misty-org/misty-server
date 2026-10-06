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

// Bounds for work one member's agent asks of another member's agent. The
// target owner pays for that work, so a request gets fewer model turns and
// less time than a run the owner starts themselves, and an unapproved request
// expires.
const (
	memberRequestModelTurns    = 12
	memberRequestExecutionMS   = 10 * 60 * 1000
	memberRequestsPerRun       = 5
	memberRequestsPerUserHour  = 60
	memberRequestMaxDepth      = 2
	memberRequestApprovalTTL   = `INTERVAL '24 hours'`
	memberRequestActiveRunList = `'queued','running','awaiting_device','awaiting_intervention','cooldown','retrying'`
)

// ExternalAgentRequesterPrefix marks requests one of the member's agents sent
// through the A2A endpoint rather than from an agent run. They count only
// toward the hourly limit.
const ExternalAgentRequesterPrefix = "a2a:"

func ExternalAgentRequester(userID, agentID string) string {
	return ExternalAgentRequesterPrefix + userID + ":" + agentID
}

type AgentMemberRequestInput struct {
	SpaceID          string
	RequesterUserID  string
	RequesterAgentID string
	RequesterRunID   string
	TargetAgentID    string
	Message          string
	IdempotencyKey   string
	Timezone         string
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
	Approval          string          `json:"approval"`
	Message           string          `json:"message"`
	CreatedAt         time.Time       `json:"created_at"`
	RunState          string          `json:"run_state"`
	RunResult         json.RawMessage `json:"run_result"`
	RunErrorMessage   string          `json:"run_error_message"`
	Replay            bool            `json:"-"`
}

const agentMemberRequestSelect = `SELECT q.id,q.space_id,s.name,q.requester_user_id,COALESCE(ru.name,''),q.requester_agent_id,q.requester_run_id,
	q.target_agent_id,COALESCE(a.name,''),q.target_owner_user_id,COALESCE(ou.name,''),q.child_run_id,q.approval,q.message,q.created_at,
	r.state,r.result,COALESCE(r.error_message,'')
	FROM agent_member_requests q JOIN spaces s ON s.id=q.space_id JOIN space_runs r ON r.id=q.child_run_id
	LEFT JOIN misty_ask_identities a ON a.id=q.target_agent_id
	LEFT JOIN users ru ON ru.id=q.requester_user_id LEFT JOIN users ou ON ou.id=q.target_owner_user_id `

func scanAgentMemberRequest(row interface{ Scan(...any) error }, out *AgentMemberRequest) error {
	return row.Scan(&out.ID, &out.SpaceID, &out.SpaceName, &out.RequesterUserID, &out.RequesterName, &out.RequesterAgentID, &out.RequesterRunID,
		&out.TargetAgentID, &out.TargetAgentName, &out.TargetOwnerUserID, &out.TargetOwnerName, &out.ChildRunID, &out.Approval, &out.Message, &out.CreatedAt,
		&out.RunState, &out.RunResult, &out.RunErrorMessage)
}

// CreateAgentMemberRequest sends work from the caller's agent run to an agent
// another member published to a shared Space. The requester's own run pays
// for asking; the delegated run is owned and paid for by the target agent's
// owner and acts only in that Space. A listing that asks first waits for its
// owner to approve before anything runs or is charged; otherwise it starts.
// A repeated idempotency key from the same run returns the original request.
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
		target, err := memberRequestTargetTx(ctx, tx, input)
		if err != nil {
			return err
		}
		if err := memberRequestLimitsTx(ctx, tx, input, target.maxOpen); err != nil {
			return err
		}
		mode := target.defaultMode
		if runModeRank(mode) > runModeRank("auto") {
			mode = "auto"
		}
		approval := "not_needed"
		if target.policy == "ask" {
			approval = "pending"
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
			VALUES($1,$2,'agent',$3,$4,$6,'delegated','queued',$5,'{}'::jsonb,$6,'direct',$3,'{}'::jsonb,'[]'::jsonb,1,'everyone',$6,
			$7,$7,$8,NULLIF($9,''),$10,'[]'::jsonb,$11,$12)`,
			runID, input.SpaceID, input.TargetAgentID, input.RequesterUserID, runInput, target.ownerUserID,
			mode, snapshot, parentRunID, depth, memberRequestModelTurns, memberRequestExecutionMS); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_member_requests(id,space_id,requester_user_id,requester_agent_id,requester_run_id,
			target_agent_id,target_owner_user_id,child_run_id,message,idempotency_key,approval) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			requestID, input.SpaceID, input.RequesterUserID, input.RequesterAgentID, input.RequesterRunID, input.TargetAgentID,
			target.ownerUserID, runID, input.Message, input.IdempotencyKey, approval); err != nil {
			return err
		}
		if approval == "not_needed" {
			if err := queueMemberRequestRunTx(ctx, tx, runID, input.SpaceID, input.TargetAgentID); err != nil {
				return err
			}
		}
		if _, err := recordSpaceEventTx(ctx, tx, input.SpaceID, input.RequesterUserID, "agent.request.created", requestID, map[string]any{
			"request_id": requestID, "requester_agent_id": input.RequesterAgentID, "target_agent_id": input.TargetAgentID, "target_agent_name": target.name,
			"target_owner_user_id": target.ownerUserID, "needs_approval": approval == "pending",
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
	ownerUserID, name, instructions, modelID, effort, defaultMode, versionID, policy string
	version                                                                          int64
	maxOpen                                                                          int
}

func memberRequestTargetTx(ctx context.Context, tx *sql.Tx, input AgentMemberRequestInput) (memberRequestTarget, error) {
	var target memberRequestTarget
	err := tx.QueryRowContext(ctx, `SELECT l.owner_user_id,l.accept_policy,l.max_open_requests,a.name,a.instructions,a.model_id,a.reasoning_effort,a.default_run_mode,a.version,v.id`+
		spaceAgentListingJoins+` JOIN misty_ask_identity_versions v ON v.agent_id=a.id AND v.version=a.version
		WHERE l.space_id=$1 AND l.agent_id=$2 AND NOT a.system_managed FOR SHARE OF l`, input.SpaceID, input.TargetAgentID).
		Scan(&target.ownerUserID, &target.policy, &target.maxOpen, &target.name, &target.instructions, &target.modelID, &target.effort, &target.defaultMode, &target.version, &target.versionID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && target.policy == "off" {
		return target, ErrAgentListingUnavailable
	}
	if err != nil {
		return target, err
	}
	if input.RequesterAgentID != "" && input.RequesterAgentID == input.TargetAgentID {
		return target, ErrAgentListingUnavailable
	}
	if !validAgentRunMode(target.defaultMode) {
		target.defaultMode = "auto"
	}
	return target, nil
}

func memberRequestLimitsTx(ctx context.Context, tx *sql.Tx, input AgentMemberRequestInput, maxOpen int) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "agent-member-request:"+input.TargetAgentID); err != nil {
		return err
	}
	// Requests the owner never answered stop holding the agent's open slots.
	if _, err := tx.ExecContext(ctx, `WITH expired AS (
		UPDATE agent_member_requests SET approval='expired',decided_at=NOW()
		WHERE target_agent_id=$1 AND approval='pending' AND created_at<NOW()-`+memberRequestApprovalTTL+` RETURNING child_run_id)
		UPDATE space_runs r SET state='rejected',error_code='approval_expired',error_message='The request expired before its owner approved it.',
		completed_at=NOW(),updated_at=NOW() FROM expired WHERE r.id=expired.child_run_id AND r.state='queued'`, input.TargetAgentID); err != nil {
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
	if open >= maxOpen {
		return ErrAgentRequestBusy
	}
	external := strings.HasPrefix(input.RequesterRunID, ExternalAgentRequesterPrefix)
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
