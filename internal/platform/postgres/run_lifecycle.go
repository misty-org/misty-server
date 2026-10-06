package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

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
