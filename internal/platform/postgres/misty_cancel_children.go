package db

import (
	"context"
	"database/sql"
)

// Parent cancellation revokes descendants in the same account boundary and
// queues runtime cancellation, including reservation and approval cleanup.
// Work the account asked another member's agent to do stops with its
// requester too, although that member owns and pays for it.
func cancelMistyChildrenTx(ctx context.Context, tx *sql.Tx, userID, runID string) error {
	rows, err := tx.QueryContext(ctx, `WITH RECURSIVE children AS (
 SELECT id,space_id FROM space_runs WHERE (parent_run_id=$1 OR input->>'parent_invocation_id'=$1 OR input->>'requester_invocation_id'=$1) AND (owner_user_id=$2 OR initiated_by_user_id=$2)
 UNION ALL SELECT r.id,r.space_id FROM space_runs r JOIN children c ON r.parent_run_id=c.id WHERE r.owner_user_id=$2 OR r.initiated_by_user_id=$2
 ) SELECT r.id,COALESCE(r.runtime_run_id,''),r.owner_user_id FROM space_runs r JOIN children c ON c.id=r.id
 WHERE r.state IN ('queued','running','awaiting_approval','awaiting_device','awaiting_intervention') FOR UPDATE OF r`, runID, userID)
	if err != nil {
		return err
	}
	type child struct{ id, runtime, owner string }
	children := []child{}
	for rows.Next() {
		var item child
		if err := rows.Scan(&item.id, &item.runtime, &item.owner); err != nil {
			rows.Close()
			return err
		}
		children = append(children, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range children {
		if err := cancelMistyRunTx(ctx, tx, item.owner, item.id, item.runtime); err != nil {
			return err
		}
	}
	return nil
}

func cancelMistyRunTx(ctx context.Context, tx *sql.Tx, userID, runID, runtimeID string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE space_runs SET state='canceled',runtime_phase='canceled',canceled_at=NOW(),completed_at=NOW(),updated_at=NOW() WHERE id=$1`, runID); err != nil {
		return err
	}
	if runtimeID != "" {
		if err := queueAgentContinuationTx(ctx, tx, userID, runID, "runtime.cancel", runtimeID, AgentContinuation{RuntimeID: runtimeID}); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_run_jobs SET state='canceled',lease_owner=NULL,lease_expires_at=NULL,completed_at=NOW(),updated_at=NOW() WHERE run_id=$1 AND state IN ('queued','leased','dispatched')`, runID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_run_contexts SET state='detached',updated_at=NOW() WHERE run_id=$1 AND state='attached'`, runID); err != nil {
		return err
	}
	if err := releasePersonalAgentRuntimeReservationsTx(ctx, tx, runID); err != nil {
		return err
	}
	return nil
}

// CancelMistyInvocationChildren commits parent cancellation, its stream event,
// descendant revocation and durable runtime delivery under one parent row lock.
// Repeated stops are harmless, and an already completed result remains completed.
func (db *Database) CancelMistyInvocationChildren(ctx context.Context, userID, invocationID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var id, state, runtimeID string
		if err := tx.QueryRowContext(ctx, `SELECT id,state,COALESCE(runtime_run_id,'') FROM ai_invocations WHERE id=$1 AND user_id=$2 FOR UPDATE`, invocationID, userID).Scan(&id, &state, &runtimeID); err != nil {
			return err
		}
		if err := cancelMistyChildrenTx(ctx, tx, userID, id); err != nil {
			return err
		}
		if state == "completed" || state == "failed" {
			return nil
		}
		// Also repair a canceled record from an older server that stopped before
		// publishing its terminal event. The row lock serializes all event writers.
		var recorded bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_invocation_events WHERE invocation_id=$1 AND event_type='invocation.canceled')`, id).Scan(&recorded); err != nil {
			return err
		}
		if !recorded {
			if _, err := tx.ExecContext(ctx, `INSERT INTO ai_invocation_events(invocation_id,sequence,event_type,payload,native_resulting_state,receipt_key) SELECT $1,n,'invocation.canceled',jsonb_build_object('id',n::text,'type','invocation.canceled','state','canceled'),'canceled','owner:cancel' FROM (SELECT COALESCE(MAX(sequence),0)+1 n FROM ai_invocation_events WHERE invocation_id=$1) seq`, id); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET state='canceled',updated_at=NOW(),canceled_at=COALESCE(canceled_at,NOW()) WHERE id=$1 AND user_id=$2`, id, userID); err != nil {
			return err
		}
		if runtimeID != "" {
			return queueAgentContinuationTx(ctx, tx, userID, id, "runtime.cancel", runtimeID, AgentContinuation{RuntimeID: runtimeID})
		}
		return nil
	})
}
