package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"
)

type SpaceAgentMessageTrigger struct {
	ID              string `json:"id"`
	AgentID         string `json:"agent_id"`
	State           string `json:"state"`
	RunID           string `json:"run_id,omitempty"`
	ErrorCode       string `json:"error_code,omitempty"`
	ErrorMessage    string `json:"error_message,omitempty"`
	TriggerKind     string `json:"trigger_kind"`
	SourceMessageID string `json:"source_message_id"`
	Created         bool   `json:"-"`
}

func (db *Database) SpaceAgentMessageTriggerIDForRun(ctx context.Context, userID, runID string) (string, error) {
	var triggerID string
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT t.id FROM space_agent_message_triggers t
			JOIN space_runs r ON r.id=t.run_id AND r.space_id=t.space_id
			WHERE r.id=$1 AND t.requesting_user_id=$2`, runID, userID).Scan(&triggerID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSpaceNotFound
	}
	return triggerID, err
}

func (db *Database) QueueSpaceAgentMessageTrigger(ctx context.Context, userID, spaceID, conversationID, sourceMessageID, agentID, triggerKind string) (*SpaceAgentMessageTrigger, error) {
	if triggerKind != "direct" && triggerKind != "mention" {
		return nil, ErrSpaceInvalid
	}
	out := &SpaceAgentMessageTrigger{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAgentsRun); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_messages WHERE id=$1 AND space_id=$2 AND COALESCE(conversation_id,'')=$3 AND sender_user_id=$4)`, sourceMessageID, spaceID, conversationID, userID).Scan(&exists); err != nil || !exists {
			return ErrSpaceInvalid
		}
		id := "agenttrigger_" + uuid.NewString()
		result, err := tx.ExecContext(ctx, `INSERT INTO space_agent_message_triggers(id,source_message_id,space_id,conversation_id,requesting_user_id,agent_id,trigger_kind)
			VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7)
			ON CONFLICT(source_message_id,agent_id,trigger_kind) DO NOTHING`, id, sourceMessageID, spaceID, conversationID, userID, agentID, triggerKind)
		if err != nil {
			return err
		}
		created, _ := result.RowsAffected()
		out.Created = created == 1
		if err := tx.QueryRowContext(ctx, `SELECT id,agent_id,state,COALESCE(run_id,''),COALESCE(error_code,''),COALESCE(error_message,''),trigger_kind,source_message_id
			FROM space_agent_message_triggers WHERE source_message_id=$1 AND agent_id=$2 AND trigger_kind=$3`, sourceMessageID, agentID, triggerKind).
			Scan(&out.ID, &out.AgentID, &out.State, &out.RunID, &out.ErrorCode, &out.ErrorMessage, &out.TriggerKind, &out.SourceMessageID); err != nil {
			return err
		}
		if out.Created {
			// Same payload shape every other agent.run.* state publishes. The
			// trigger struct alone omits conversation_id, so subscribers scoping
			// events to one conversation dropped the queued state and only saw
			// the Agent from "working" onward.
			_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "agent.run.queued", out.ID, map[string]any{
				"trigger_id": out.ID, "state": out.State, "run_id": out.RunID,
				"agent_id": out.AgentID, "conversation_id": conversationID,
				"source_message_id": out.SourceMessageID, "error_code": "", "error_message": "",
			})
		}
		return err
	})
	return out, err
}

func (db *Database) UpdateSpaceAgentMessageTrigger(ctx context.Context, triggerID, state, runID, errorCode, errorMessage string) error {
	switch state {
	case "queued", "working", "awaiting_approval", "completed", "failed", "canceled", "retrying":
	default:
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var spaceID, userID, agentID string
		var conversationID, sourceMessageID sql.NullString
		err := tx.QueryRowContext(ctx, `UPDATE space_agent_message_triggers
			SET state=$2,run_id=NULLIF($3,''),error_code=NULLIF($4,''),error_message=NULLIF($5,''),updated_at=NOW()
			WHERE id=$1 RETURNING space_id,conversation_id,requesting_user_id,agent_id,source_message_id`,
			triggerID, state, strings.TrimSpace(runID), strings.TrimSpace(errorCode), strings.TrimSpace(errorMessage)).
			Scan(&spaceID, &conversationID, &userID, &agentID, &sourceMessageID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "agent.run."+state, triggerID, map[string]any{
			"trigger_id": triggerID, "state": state, "run_id": strings.TrimSpace(runID),
			"agent_id": agentID, "conversation_id": conversationID.String,
			"source_message_id": sourceMessageID.String, "error_code": strings.TrimSpace(errorCode),
			"error_message": strings.TrimSpace(errorMessage),
		})
		return err
	})
}

func (db *Database) DirectConversationAgentID(ctx context.Context, userID, spaceID, conversationID string) (string, error) {
	var agentID string
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, conversationID); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT direct_agent_id FROM space_conversations
			WHERE id=$1 AND space_id=$2 AND kind='direct'`, conversationID, spaceID).Scan(&agentID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	return agentID, err
}
