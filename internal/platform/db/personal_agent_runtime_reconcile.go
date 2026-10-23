package db

import (
	"context"
	"database/sql"
	"github.com/google/uuid"
	"time"
)

// ReconcileStalePersonalAgentTaskRuns preserves the pinned runtime. A missed
// heartbeat is never permission to restart the prompt and repeat its effects.
func (db *Database) ReconcileStalePersonalAgentTaskRuns(ctx context.Context, staleBefore time.Time, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	count := 0
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT r.id,r.owner_user_id,r.runtime_run_id FROM agent_run_jobs j JOIN space_runs r ON r.id=j.run_id
   WHERE j.state='dispatched' AND r.execution_owner='go' AND r.state IN ('running','awaiting_approval','awaiting_device','awaiting_intervention') AND r.runtime_run_id<>''
    AND COALESCE(r.runtime_heartbeat_at,r.updated_at)<$1
   ORDER BY COALESCE(r.runtime_heartbeat_at,r.updated_at) FOR UPDATE OF j,r SKIP LOCKED LIMIT $2`, staleBefore, limit)
		if err != nil {
			return err
		}
		type item struct{ runID, userID, runtimeID string }
		items := []item{}
		for rows.Next() {
			var i item
			if err := rows.Scan(&i.runID, &i.userID, &i.runtimeID); err != nil {
				rows.Close()
				return err
			}
			items = append(items, i)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, i := range items {
			if err := queueAgentContinuationTx(ctx, tx, i.userID, i.runID, "runtime.reconcile", i.runtimeID+":"+uuid.NewString(), AgentContinuation{RuntimeID: i.runtimeID}); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE space_runs SET runtime_phase=CASE WHEN state='running' THEN 'recovering' ELSE runtime_phase END,runtime_heartbeat_at=NOW(),updated_at=NOW() WHERE id=$1`, i.runID); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

func (db *Database) RecordAgentRuntimeStatus(ctx context.Context, runID, runtimeID, status string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		switch status {
		case "pending", "running":
			_, err := tx.ExecContext(ctx, `UPDATE space_runs SET runtime_heartbeat_at=NOW(),runtime_phase=CASE WHEN runtime_phase='recovering' THEN 'working' ELSE runtime_phase END,updated_at=NOW()
    WHERE id=$1 AND runtime_run_id=$2 AND state IN ('running','awaiting_approval','awaiting_device','awaiting_intervention')`, runID, runtimeID)
			return err
		case "completed", "failed", "cancelled", "missing":
			// The durable completion callback, not a model's final text or workflow exit,
			// is authoritative for successful completion. Preserve all existing outputs.
			result, err := tx.ExecContext(ctx, `UPDATE space_runs SET state='failed',runtime_phase='needs_attention',
    error_code='runtime_reconciliation_required',error_message='The pinned runtime stopped without a confirmed completion. Review completed and uncertain actions before recovery.',
    completed_at=NOW(),updated_at=NOW() WHERE id=$1 AND runtime_run_id=$2 AND state IN ('running','awaiting_approval','awaiting_device','awaiting_intervention')`, runID, runtimeID)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil || n == 0 {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE agent_run_jobs SET state='failed',completed_at=NOW(),updated_at=NOW() WHERE run_id=$1 AND state='dispatched'`, runID); err != nil {
				return err
			}
			return releasePersonalAgentRuntimeReservationsTx(ctx, tx, runID)
		default:
			return ErrSpaceInvalid
		}
	})
}
