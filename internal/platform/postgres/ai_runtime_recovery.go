package db

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"time"
)

// ReconcileStaleAIInvocations schedules an observation of the original runtime.
// Neither a missed heartbeat nor an observation error admits another start.
func (db *Database) ReconcileStaleAIInvocations(ctx context.Context, staleBefore time.Time, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	count := 0
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT i.id,i.user_id,i.runtime_run_id,clock_timestamp() FROM ai_invocations i WHERE COALESCE(i.agent_run_id,'')='' AND i.runtime_run_id<>'' AND i.state IN ('running','awaiting_approval','awaiting_device','awaiting_intervention','awaiting_timer') AND GREATEST(COALESCE(i.runtime_observed_at,i.created_at),COALESCE(i.runtime_heartbeat_at,i.created_at))<$1 AND NOT EXISTS(SELECT 1 FROM agent_runtime_deliveries d WHERE d.run_id=i.id AND d.operation='runtime.reconcile' AND d.state IN ('pending','leased')) ORDER BY GREATEST(COALESCE(i.runtime_observed_at,i.created_at),COALESCE(i.runtime_heartbeat_at,i.created_at)),i.id FOR UPDATE OF i SKIP LOCKED LIMIT $2`, staleBefore, limit)
		if err != nil {
			return err
		}
		type item struct {
			run, user, runtime string
			observedAfter      time.Time
		}
		items := []item{}
		for rows.Next() {
			var i item
			if err := rows.Scan(&i.run, &i.user, &i.runtime, &i.observedAfter); err != nil {
				rows.Close()
				return err
			}
			items = append(items, i)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, i := range items {
			// Recheck with a fresh statement snapshot after the row lock. A worker
			// that acquired it just after another transaction committed must see that
			// transaction's outbox row, not the selection query's older snapshot.
			var pending bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_runtime_deliveries WHERE run_id=$1 AND operation='runtime.reconcile' AND state IN ('pending','leased'))`, i.run).Scan(&pending); err != nil {
				return err
			}
			if pending {
				continue
			}
			if err := queueAgentContinuationTx(ctx, tx, i.user, i.run, "runtime.reconcile", i.runtime+":"+uuid.NewString(), AgentContinuation{RuntimeID: i.runtime, ObservedAfter: &i.observedAfter}); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

// RecordAIInvocationRuntimeStatus returns true only when a stopped SDK or routine run needs
// proof-based completion. An engine exit is never proof that an action succeeded.
func (db *Database) RecordAIInvocationRuntimeStatus(ctx context.Context, userID, runID, runtimeID, status string, observedAfter time.Time) (bool, error) {
	if observedAfter.IsZero() {
		return false, ErrSpaceInvalid
	}
	terminal := false
	switch status {
	case "pending", "running":
	case "completed", "failed", "cancelled", "missing":
		terminal = true
	default:
		return false, ErrSpaceInvalid
	}
	reconcileSDK := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var state, surface string
		var updated time.Time
		err := tx.QueryRowContext(ctx, `SELECT state,surface_id,updated_at FROM ai_invocations WHERE id=$1 AND user_id=$2 AND runtime_run_id=$3 AND COALESCE(agent_run_id,'')='' FOR UPDATE`, runID, userID, runtimeID).Scan(&state, &surface, &updated)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if state == "completed" || state == "failed" || state == "canceled" {
			return nil
		}
		// A callback or a new wait committed while the status request was in flight.
		// Its newer state wins; a later observation can inspect the runtime again.
		if terminal && updated.After(observedAfter) {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET runtime_observed_at=NOW(),runtime_observed_status=$2 WHERE id=$1`, runID, status); err != nil {
			return err
		}
		if !terminal {
			return nil
		}
		if surface == "sdk" || surface == "routine" {
			reconcileSDK = true
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET state='failed',updated_at=NOW() WHERE id=$1`, runID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO ai_invocation_events(invocation_id,sequence,event_type,payload,receipt_key) SELECT $1,n,'invocation.failed',jsonb_build_object('id',n::text,'type','invocation.failed','state','failed','code','runtime_reconciliation_required','error','The pinned runtime stopped without a confirmed completion. Review completed and uncertain actions before recovery.'),$2 FROM (SELECT COALESCE(MAX(sequence),0)+1 n FROM ai_invocation_events WHERE invocation_id=$1) seq ON CONFLICT(invocation_id,receipt_key) WHERE receipt_key IS NOT NULL DO NOTHING`, runID, "runtime-reconciliation:"+runtimeID)
		return err
	})
	return reconcileSDK, err
}
