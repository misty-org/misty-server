package db

import (
	"context"
	"database/sql"
	"encoding/json"
)

type AgentExecutionLease struct {
	AgentID     string `json:"agent_id"`
	SpaceID     string `json:"space_id"`
	TaskID      string `json:"task_id"`
	WindowLabel string `json:"window_label"`
	Renew       bool   `json:"renew"`
}

func (db *Database) AcquireAgentExecution(ctx context.Context, userID string, input AgentExecutionLease) error {
	input.SpaceID = ""
	if AppAuthorityFromContext(ctx) != nil || input.TaskID == "" || len(input.TaskID) > 128 || input.WindowLabel == "" || len(input.WindowLabel) > 128 {
		return ErrSpaceForbidden
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if _, err := askExecutionContextTx(ctx, tx, userID, input.SpaceID, input.AgentID); err != nil {
			return err
		}
		if input.Renew {
			result, err := tx.ExecContext(ctx, `UPDATE misty_agent_execution_leases SET expires_at=NOW()+INTERVAL '25 seconds' WHERE owner_user_id=$1 AND agent_id=$2 AND COALESCE(space_id,'')=$3 AND task_id=$4 AND window_label=$5 AND expires_at>NOW()`, userID, input.AgentID, input.SpaceID, input.TaskID, input.WindowLabel)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err == nil && n != 1 {
				return ErrSpaceConflict
			}
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO misty_agent_execution_leases(owner_user_id,agent_id,space_id,task_id,window_label,expires_at) VALUES($1,$2,NULLIF($3,''),$4,$5,NOW()+INTERVAL '25 seconds') ON CONFLICT(owner_user_id,agent_id) DO UPDATE SET space_id=EXCLUDED.space_id,task_id=EXCLUDED.task_id,window_label=EXCLUDED.window_label,expires_at=EXCLUDED.expires_at WHERE misty_agent_execution_leases.expires_at<=NOW() OR (misty_agent_execution_leases.task_id=EXCLUDED.task_id AND misty_agent_execution_leases.window_label=EXCLUDED.window_label AND misty_agent_execution_leases.space_id IS NOT DISTINCT FROM EXCLUDED.space_id)`, userID, input.AgentID, input.SpaceID, input.TaskID, input.WindowLabel)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err == nil && n != 1 {
			return ErrSpaceConflict
		}
		return err
	})
}
func (db *Database) ReleaseAgentExecution(ctx context.Context, userID, taskID string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM misty_agent_execution_leases WHERE owner_user_id=$1 AND task_id=$2`, userID, taskID)
		return err
	})
}
func (db *Database) ValidateNativeAgentExecution(ctx context.Context, record *AIInvocationRecord) error {
	var input struct {
		AgentID     string `json:"agent_id"`
		Mode        string `json:"execution_mode"`
		TaskID      string `json:"task_id"`
		WindowLabel string `json:"window_label"`
	}
	if json.Unmarshal(record.RequestPayload, &input) != nil {
		return ErrSpaceInvalid
	}
	if input.AgentID == "" || input.Mode == "" {
		return nil
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(record.UserID), func(tx *sql.Tx) error {
		return validateNativeAgentExecutionTx(ctx, tx, record.UserID, record.SpaceID, record.RequestPayload)
	})
}

func validateNativeAgentExecutionTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, payload json.RawMessage) error {
	spaceID = ""
	var input struct {
		AgentID     string `json:"agent_id"`
		Mode        string `json:"execution_mode"`
		TaskID      string `json:"task_id"`
		WindowLabel string `json:"window_label"`
	}
	if json.Unmarshal(payload, &input) != nil {
		return ErrSpaceInvalid
	}
	if input.AgentID == "" || input.Mode == "" {
		return nil
	}
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM misty_ask_identities a WHERE a.id=$2 AND a.owner_user_id=$1 AND a.enabled AND a.deleted_at IS NULL AND ($6='user' OR EXISTS(SELECT 1 FROM misty_agent_execution_leases l WHERE l.owner_user_id=$1 AND l.agent_id=$2 AND COALESCE(l.space_id,'')=$3 AND l.task_id=$4 AND l.window_label=$5 AND l.expires_at>NOW())))`, userID, input.AgentID, spaceID, input.TaskID, input.WindowLabel, input.Mode).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrSpaceForbidden
	}
	return nil
}
