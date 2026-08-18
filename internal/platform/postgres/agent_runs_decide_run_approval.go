package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

func (db *Database) DecideRunApproval(ctx context.Context, userID, runID string, approved bool) (*SpaceRun, error) {
	out := &SpaceRun{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := scanSpaceRun(tx.QueryRowContext(ctx, `SELECT `+spaceRunColumns+` FROM space_runs WHERE id=$1 FOR UPDATE`, runID), out); err != nil {
			return err
		}
		if out.RequestingMemberID != userID || out.State != "awaiting_approval" {
			return ErrSpaceForbidden
		}
		if err := requireSpacePermissionTx(ctx, tx, userID, out.SpaceID, PermissionAgentsRun); err != nil {
			return err
		}
		workflow, err := loadWorkflowVersionTx(ctx, tx, out.WorkflowVersionID)
		if err != nil {
			return err
		}
		if err := authorizeAgentWorkflowRequirementsTx(ctx, tx, userID, out.SpaceID, out.AgentInstanceID, workflow.Metadata); err != nil {
			return err
		}
		if err := requireRunResourceEnabledTx(ctx, tx, out); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_run_approvals SET state='expired' WHERE run_id=$1 AND state='pending' AND expires_at<=NOW()`, runID); err != nil {
			return err
		}
		var pending int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM space_run_approvals WHERE run_id=$1 AND state='pending' AND expires_at>NOW()`, runID).Scan(&pending); err != nil {
			return err
		}
		if pending == 0 {
			return ErrSpaceConflict
		}
		decision, state := "rejected", "rejected"
		if approved {
			decision, state = "approved", "running"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_run_approvals SET state=$1,decided_by_user_id=$2,decided_at=NOW() WHERE run_id=$3 AND state='pending' AND expires_at>NOW()`, decision, userID, runID); err != nil {
			return err
		}
		actionState := "canceled"
		if approved {
			actionState = "approved"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_run_actions SET state=$1 WHERE run_id=$2 AND state='proposed'`, actionState, runID); err != nil {
			return err
		}
		if err := scanSpaceRun(tx.QueryRowContext(ctx, `UPDATE space_runs SET state=$1,completed_at=CASE WHEN $1='rejected' THEN NOW() ELSE completed_at END,updated_at=NOW() WHERE id=$2 RETURNING `+spaceRunColumns, state, runID), out); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, out.SpaceID, userID, "agent.run.approval."+decision, runID, map[string]bool{"approved": approved})
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) CreateAgentConversation(ctx context.Context, userID, spaceID, agentID, title string) (*AgentConversation, error) {
	if _, err := db.EnsureAgentInstance(ctx, userID, spaceID, agentID); err != nil {
		return nil, err
	}
	out := &AgentConversation{ID: "spaceconversation_" + uuid.NewString(), SpaceID: spaceID, OwnerUserID: userID, AgentID: agentID, Title: strings.TrimSpace(title)}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAgentsRun); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `INSERT INTO space_agent_conversations(id,space_id,owner_user_id,agent_id,title) SELECT $1,$2,$3,a.id,$4 FROM space_agents a WHERE a.id=$5 AND a.space_id=$2 AND a.enabled RETURNING created_at,updated_at`, out.ID, spaceID, userID, out.Title, agentID).Scan(&out.CreatedAt, &out.UpdatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) AppendAgentConversationEvent(ctx context.Context, userID, conversationID, eventType string, data json.RawMessage) (*AgentConversationEvent, error) {
	if eventType != "user_message" && eventType != "agent_message" && eventType != "run" && eventType != "error" || !validJSONObject(data) {
		return nil, ErrSpaceInvalid
	}
	out := &AgentConversationEvent{ConversationID: conversationID, UserID: userID, EventType: eventType, Data: data}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var spaceID string
		if err := tx.QueryRowContext(ctx, `SELECT space_id FROM space_agent_conversations WHERE id=$1 AND owner_user_id=$2 AND deleted_at IS NULL`, conversationID, userID).Scan(&spaceID); err != nil {
			return err
		}
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAgentsRun); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO space_agent_conversation_events(conversation_id,user_id,event_type,data) VALUES($1,$2,$3,$4) RETURNING id,created_at`, conversationID, userID, eventType, data).Scan(&out.ID, &out.CreatedAt); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE space_agent_conversations SET updated_at=NOW() WHERE id=$1`, conversationID)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) AgentConversationEvents(ctx context.Context, userID, conversationID string) ([]AgentConversationEvent, error) {
	items := []AgentConversationEvent{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var spaceID string
		if err := tx.QueryRowContext(ctx, `SELECT space_id FROM space_agent_conversations WHERE id=$1 AND owner_user_id=$2 AND deleted_at IS NULL`, conversationID, userID).Scan(&spaceID); err != nil {
			return err
		}
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAgentsRun); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,conversation_id,user_id,event_type,data,created_at FROM space_agent_conversation_events WHERE conversation_id=$1 AND user_id=$2 ORDER BY id`, conversationID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentConversationEvent
			if err := rows.Scan(&item.ID, &item.ConversationID, &item.UserID, &item.EventType, &item.Data, &item.CreatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return items, err
}

func scanSpaceRun(scanner interface{ Scan(...any) error }, out *SpaceRun) error {
	return scanner.Scan(&out.ID, &out.SpaceID, &out.ResourceKind, &out.ResourceID, &out.InitiatedByUserID, &out.BillingUserID, &out.TriggerKind, &out.State, &out.Input, &out.Result, &out.ErrorCode, &out.CreatedAt, &out.CompletedAt, &out.RequestingMemberID, &out.SourceConversationID, &out.SourceType, &out.AgentID, &out.WorkflowIdentifier, &out.WorkflowVersionID, &out.WorkflowVersion, &out.CapabilityID, &out.Progress, &out.Outputs, &out.Artifacts, &out.ErrorMessage, &out.RetryOfRunID, &out.CanceledAt, &out.UpdatedAt, &out.AgentInstanceID, &out.AgentVersionID, &out.Attempt, &out.NextRetryAt, &out.SourceTaskID, &out.ActionEnvelope, &out.ConversationScopeKind, &out.ScopeConversationID, &out.SourceMessageID, &out.RuntimeKind, &out.RuntimeRunID, &out.RuntimePhase, &out.RuntimeHeartbeatAt)
}
