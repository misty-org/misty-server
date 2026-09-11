package db

import (
	"context"
	"database/sql"
)

// Parent cancellation revokes descendants in the same owner/Space boundary and
// queues runtime cancellation, including reservation and approval cleanup.
func cancelMistyChildrenTx(ctx context.Context, tx *sql.Tx, userID, runID string) error {
	rows, err := tx.QueryContext(ctx, `WITH RECURSIVE children AS (
 SELECT id,space_id FROM space_runs WHERE (parent_run_id=$1 OR input->>'parent_invocation_id'=$1) AND owner_user_id=$2
 UNION ALL SELECT r.id,r.space_id FROM space_runs r JOIN children c ON r.parent_run_id=c.id AND r.space_id=c.space_id WHERE r.owner_user_id=$2
 ) SELECT r.id,COALESCE(r.runtime_run_id,'') FROM space_runs r JOIN children c ON c.id=r.id
 WHERE r.state IN ('queued','running','awaiting_approval','awaiting_device','awaiting_intervention') FOR UPDATE OF r`, runID, userID)
	if err != nil {
		return err
	}
	type child struct{ id, runtime string }
	children := []child{}
	for rows.Next() {
		var item child
		if err := rows.Scan(&item.id, &item.runtime); err != nil {
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
		if err := cancelMistyRunTx(ctx, tx, userID, item.id, item.runtime); err != nil {
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
	if _, err := tx.ExecContext(ctx, `UPDATE agent_run_tool_approvals SET state='denied',decided_by_user_id=$2,decided_at=NOW() WHERE run_id=$1 AND state='pending'`, runID, userID); err != nil {
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

func cancelMistySpaceTx(ctx context.Context, tx *sql.Tx, spaceID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,owner_user_id,COALESCE(runtime_run_id,'') FROM space_runs WHERE space_id=$1 AND state IN ('queued','running','awaiting_approval','awaiting_device','awaiting_intervention') FOR UPDATE`, spaceID)
	if err != nil {
		return err
	}
	type run struct{ id, user, runtime string }
	runs := []run{}
	for rows.Next() {
		var item run
		if err := rows.Scan(&item.id, &item.user, &item.runtime); err != nil {
			rows.Close()
			return err
		}
		runs = append(runs, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range runs {
		if err := cancelMistyRunTx(ctx, tx, item.user, item.id, item.runtime); err != nil {
			return err
		}
	}
	invocationRows, err := tx.QueryContext(ctx, `SELECT id,user_id,COALESCE(runtime_run_id,'') FROM ai_invocations WHERE space_id=$1 AND state IN ('queued','running','awaiting_approval','awaiting_device','awaiting_intervention','awaiting_timer') FOR UPDATE`, spaceID)
	if err != nil {
		return err
	}
	invocations := []run{}
	for invocationRows.Next() {
		var item run
		if err := invocationRows.Scan(&item.id, &item.user, &item.runtime); err != nil {
			invocationRows.Close()
			return err
		}
		invocations = append(invocations, item)
	}
	err = invocationRows.Err()
	invocationRows.Close()
	if err != nil {
		return err
	}
	for _, item := range invocations {
		if item.runtime != "" {
			if err := queueAgentContinuationTx(ctx, tx, item.user, item.id, "runtime.cancel", item.runtime, AgentContinuation{RuntimeID: item.runtime}); err != nil {
				return err
			}
		}
		if err := releasePersonalAgentRuntimeReservationsTx(ctx, tx, item.id); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE ai_invocations SET state='canceled',updated_at=NOW() WHERE space_id=$1 AND state IN ('queued','running','awaiting_approval','awaiting_device','awaiting_intervention','awaiting_timer')`, spaceID)
	return err
}

func (db *Database) CancelMistyInvocationChildren(ctx context.Context, userID, invocationID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var id string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM ai_invocations WHERE id=$1 AND user_id=$2 FOR UPDATE`, invocationID, userID).Scan(&id); err != nil {
			return err
		}
		if err := cancelMistyChildrenTx(ctx, tx, userID, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET state='canceled',updated_at=NOW() WHERE id=$1 AND user_id=$2 AND state IN ('queued','running','awaiting_approval','awaiting_device','awaiting_intervention','awaiting_timer')`, id, userID)
		return err
	})
}
