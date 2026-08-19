package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (db *Database) CancelSpaceRun(ctx context.Context, userID, runID string) (*SpaceRun, error) {
	out := &SpaceRun{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var spaceID, requester string
		if err := tx.QueryRowContext(ctx, `SELECT space_id,requesting_member_id FROM space_runs WHERE id=$1`, runID).Scan(&spaceID, &requester); err != nil {
			return err
		}
		if requester != userID {
			return ErrSpaceForbidden
		}
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAgentsRun); err != nil {
			return err
		}
		if err := scanSpaceRun(tx.QueryRowContext(ctx, `UPDATE space_runs SET state='canceled',canceled_at=NOW(),completed_at=NOW(),updated_at=NOW() WHERE id=$1 AND state IN ('queued','running','awaiting_approval','cooldown') RETURNING `+spaceRunColumns, runID), out); err != nil {
			return err
		}
		_, _ = tx.ExecContext(ctx, `UPDATE space_run_approvals SET state='canceled',decided_by_user_id=$1,decided_at=NOW() WHERE run_id=$2 AND state='pending'`, userID, runID)
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "agent.run.canceled", runID, map[string]any{})
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) RetrySpaceRun(ctx context.Context, userID, runID string) (*SpaceRun, error) {
	previous, err := db.SpaceRun(ctx, userID, runID)
	if err != nil {
		return nil, err
	}
	if previous.RequestingMemberID != userID || previous.State != "failed" && previous.State != "canceled" && previous.State != "completed_with_errors" {
		return nil, ErrSpaceForbidden
	}
	// Suggestion and reminder runs are exact-payload executions. They must be
	// retried through their source item, never through the free-form Agent runner.
	if previous.SourceType == "suggestion" || previous.SourceType == "follow_up" {
		return nil, ErrSpaceInvalid
	}
	out := *previous
	out.ID, out.State, out.Progress, out.Attempt = "run_"+uuid.NewString(), "running", 0, 1
	out.Result, out.Outputs, out.Artifacts = json.RawMessage(`{}`), json.RawMessage(`{}`), json.RawMessage(`[]`)
	out.ErrorCode, out.ErrorMessage, out.RetryOfRunID = "", "", previous.ID
	out.CompletedAt, out.CanceledAt = nil, nil
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, previous.SpaceID, PermissionAgentsRun); err != nil {
			return err
		}
		if err := requireRunResourceEnabledTx(ctx, tx, previous); err != nil {
			return err
		}
		var workflow *WorkflowVersion
		var capability *WorkflowCapability
		if previous.WorkflowVersionID != "" {
			workflow, err = loadWorkflowVersionTx(ctx, tx, previous.WorkflowVersionID)
			if err != nil {
				return err
			}
			if err := authorizeAgentWorkflowRequirementsTx(ctx, tx, userID, previous.SpaceID, previous.AgentInstanceID, workflow.Metadata); err != nil {
				return err
			}
			capability, err = selectWorkflowCapability(workflow.Metadata, out.CapabilityID)
			if err != nil || TestingValidateCapabilityInput(*capability, out.Input) != nil {
				return ErrSpaceInvalid
			}
			if capability.ConfirmationRequired && !capability.Destructive {
				out.State = "awaiting_approval"
			}
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO space_runs(id,space_id,resource_kind,resource_id,initiated_by_user_id,billing_user_id,trigger_kind,state,input,requesting_member_id,source_conversation_id,source_type,agent_id,workflow_identifier,workflow_version_id,workflow_version,capability_id,outputs,artifacts,retry_of_run_id,agent_instance_id,agent_version_id,attempt,conversation_scope_kind,scope_conversation_id,source_message_id)
			VALUES($1,$2,'agent',$3,$4,$4,'retry',$5,$6,$4,NULLIF($7,''),$8,$3,NULLIF($9,''),NULLIF($10,''),NULLIF($11,''),$12,'{}'::jsonb,'[]'::jsonb,$13,$14,$15,1,$16,NULLIF($17,''),NULLIF($18,'')) RETURNING created_at,updated_at`, out.ID, out.SpaceID, out.ResourceID, userID, out.State, out.Input, out.SourceConversationID, out.SourceType, out.WorkflowIdentifier, out.WorkflowVersionID, out.WorkflowVersion, out.CapabilityID, previous.ID, previous.AgentInstanceID, previous.AgentVersionID, previous.ConversationScopeKind, previous.ScopeConversationID, previous.SourceMessageID).Scan(&out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		if out.State == "awaiting_approval" && capability != nil && workflow != nil {
			return insertRunApprovalTx(ctx, tx, out.ID, userID, capability, workflow.ID)
		}
		return nil
	})
	return &out, err
}

func requireRunResourceEnabledTx(ctx context.Context, tx *sql.Tx, run *SpaceRun) error {
	var enabled bool
	var err error
	switch run.ResourceKind {
	case "agent":
		err = tx.QueryRowContext(ctx, `SELECT a.enabled AND a.deleted_at IS NULL AND EXISTS(
			SELECT 1 FROM space_members m WHERE m.space_id=$2 AND m.user_id=a.owner_user_id)
			FROM personal_agents a WHERE a.id=$1`, run.ResourceID, run.SpaceID).Scan(&enabled)
	default:
		return ErrSpaceInvalid
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSpaceNotFound
	}
	if err != nil {
		return err
	}
	if !enabled {
		return ErrSpaceInvalid
	}
	return nil
}

func (db *Database) RunApprovals(ctx context.Context, userID, runID string) ([]RunApproval, error) {
	if _, err := db.SpaceRun(ctx, userID, runID); err != nil {
		return nil, err
	}
	items := []RunApproval{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,run_id,requested_from_user_id,COALESCE(decided_by_user_id,''),action_summary,proposed_actions,state,created_at,decided_at,expires_at FROM space_run_approvals WHERE run_id=$1 AND requested_from_user_id=$2 ORDER BY created_at`, runID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item RunApproval
			if err := rows.Scan(&item.ID, &item.RunID, &item.RequestedFromUserID, &item.DecidedByUserID, &item.ActionSummary, &item.ProposedActions, &item.State, &item.CreatedAt, &item.DecidedAt, &item.ExpiresAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) RunActions(ctx context.Context, userID, runID string) ([]RunAction, error) {
	if _, err := db.SpaceRun(ctx, userID, runID); err != nil {
		return nil, err
	}
	items := []RunAction{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,run_id,action_kind,summary,details,destructive,state,performed_at,created_at FROM space_run_actions WHERE run_id=$1 ORDER BY created_at`, runID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item RunAction
			if err := rows.Scan(&item.ID, &item.RunID, &item.ActionKind, &item.Summary, &item.Details, &item.Destructive, &item.State, &item.PerformedAt, &item.CreatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) RecordRunAction(ctx context.Context, runID, kind, summary string, details json.RawMessage, destructive bool, state string) error {
	if !validJSONObject(details) {
		details = json.RawMessage(`{}`)
	}
	if state == "" {
		state = "completed"
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var performed any
		if state == "completed" || state == "failed" {
			performed = time.Now().UTC()
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO space_run_actions(id,run_id,action_kind,summary,details,destructive,state,performed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, "runaction_"+uuid.NewString(), runID, kind, summary, details, destructive, state, performed)
		return err
	})
}

// ClaimRunResponsePublication serializes completion delivery for a run. The
// claim is retryable after a failed delivery and prevents device completion
// replays from posting duplicate conversation responses.
func (db *Database) ClaimRunResponsePublication(ctx context.Context, runID string) (string, bool, error) {
	actionID := ""
	claimed := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "run-response:"+runID); err != nil {
			return err
		}
		var state string
		err := tx.QueryRowContext(ctx, `SELECT id,state FROM space_run_actions WHERE run_id=$1 AND action_kind='conversation_response' ORDER BY created_at DESC LIMIT 1`, runID).Scan(&actionID, &state)
		if err == nil {
			if state == "completed" || state == "approved" {
				return nil
			}
			_, err = tx.ExecContext(ctx, `UPDATE space_run_actions SET state='approved',details='{}'::jsonb,performed_at=NULL WHERE id=$1`, actionID)
			claimed = err == nil
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var terminal bool
		if err := tx.QueryRowContext(ctx, `SELECT state IN ('completed','failed','canceled') FROM space_runs WHERE id=$1`, runID).Scan(&terminal); err != nil {
			return err
		}
		if !terminal {
			return nil
		}
		actionID = "runaction_" + uuid.NewString()
		_, err = tx.ExecContext(ctx, `INSERT INTO space_run_actions(id,run_id,action_kind,summary,details,destructive,state) VALUES($1,$2,'conversation_response','Deliver terminal result to the source conversation','{}'::jsonb,FALSE,'approved')`, actionID, runID)
		claimed = err == nil
		return err
	})
	return actionID, claimed, err
}

func (db *Database) FinishRunResponsePublication(ctx context.Context, actionID, state string, details json.RawMessage) error {
	if actionID == "" || state != "completed" && state != "failed" || !validJSONObject(details) {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE space_run_actions SET state=$1,details=$2,performed_at=NOW() WHERE id=$3 AND action_kind='conversation_response' AND state='approved'`, state, details, actionID)
		return err
	})
}

func (db *Database) AgentConversations(ctx context.Context, userID string) ([]AgentConversation, error) {
	items := []AgentConversation{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT c.id,c.space_id,c.owner_user_id,c.agent_id,a.name,c.title,c.created_at,c.updated_at FROM space_agent_conversations c JOIN space_agents a ON a.id=c.agent_id JOIN space_members m ON m.space_id=c.space_id AND m.user_id=$1 WHERE c.owner_user_id=$1 AND c.deleted_at IS NULL ORDER BY c.updated_at DESC`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentConversation
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.OwnerUserID, &item.AgentID, &item.AgentName, &item.Title, &item.CreatedAt, &item.UpdatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) AgentConversationByID(ctx context.Context, userID, conversationID string) (*AgentConversation, error) {
	out := &AgentConversation{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT c.id,c.space_id,c.owner_user_id,c.agent_id,a.name,c.title,c.created_at,c.updated_at FROM space_agent_conversations c JOIN space_agents a ON a.id=c.agent_id WHERE c.id=$1 AND c.owner_user_id=$2 AND c.deleted_at IS NULL`, conversationID, userID).Scan(&out.ID, &out.SpaceID, &out.OwnerUserID, &out.AgentID, &out.AgentName, &out.Title, &out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		return requireSpacePermissionTx(ctx, tx, userID, out.SpaceID, PermissionAgentsRun)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}
