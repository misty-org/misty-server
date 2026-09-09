package db

import (
	"context"
	"database/sql"
)

// These are service-transaction reads, not public views. Run/context identities
// remain in their original tables; the shared journal never aliases accounts.
const interventionRunsSQL = `(SELECT id,user_id,COALESCE(space_id,'') AS space_id,runtime_run_id,state,expires_at FROM ai_invocations WHERE COALESCE(agent_run_id,'')=''
 UNION ALL SELECT id,owner_user_id AS user_id,space_id,runtime_run_id,state,'infinity'::timestamptz AS expires_at FROM space_runs)`
const interventionContextsSQL = `(SELECT id,invocation_id AS run_id,user_id,COALESCE(space_id,'') AS space_id,device_id,kind,opaque_ref,display_name,capabilities,state,expires_at FROM ai_invocation_contexts
 UNION ALL SELECT id,run_id,owner_user_id AS user_id,space_id,device_id,kind,opaque_ref,display_name,capabilities,state,expires_at FROM agent_run_contexts)`

func lockAgentInterventionParentTx(ctx context.Context, tx *sql.Tx, user, run string) (string, error) {
	query := `SELECT runtime_run_id FROM space_runs WHERE id=$1 AND owner_user_id=$2 AND state='awaiting_intervention' FOR UPDATE`
	if sdkRunIdentity(run) {
		query = `SELECT runtime_run_id FROM ai_invocations WHERE id=$1 AND user_id=$2 AND state='awaiting_intervention' AND COALESCE(agent_run_id,'')='' FOR UPDATE`
	}
	var runtime string
	if err := tx.QueryRowContext(ctx, query, run, user).Scan(&runtime); err != nil {
		return "", ErrSpaceConflict
	}
	return runtime, nil
}
func agentInterventionStateTx(ctx context.Context, tx *sql.Tx, user, run, wait, state, phase, message string) error {
	if sdkRunIdentity(run) {
		if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET state=$2,updated_at=NOW() WHERE id=$1`, run, state); err != nil {
			return err
		}
		return aiDeviceWaitStatusTx(ctx, tx, run, wait, phase, message)
	}
	var space string
	if err := tx.QueryRowContext(ctx, `UPDATE space_runs SET state=$2,runtime_phase=$3,updated_at=NOW() WHERE id=$1 AND owner_user_id=$4 RETURNING space_id`, run, state, phase, user).Scan(&space); err != nil {
		return err
	}
	_, err := recordSpaceEventTx(ctx, tx, space, user, "agent.run."+phase, run, map[string]any{"run_id": run, "wait_id": wait, "phase": phase})
	return err
}

func agentInterventionAuthorityTx(ctx context.Context, tx *sql.Tx, user, run, runtime string) error {
	if _, _, err := deviceRunAuthorityTx(ctx, tx, user, run, &runtime, "browser.inspect", true); err != nil {
		return err
	}
	if sdkRunIdentity(run) {
		return nil
	}
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_runs r JOIN misty_ask_identities a ON a.id=r.agent_id AND a.owner_user_id=r.owner_user_id JOIN agent_run_jobs j ON j.run_id=r.id AND j.agent_id=r.agent_id LEFT JOIN space_tasks t ON t.id=j.task_id WHERE r.id=$1 AND r.owner_user_id=$2 AND a.enabled AND a.deleted_at IS NULL AND j.state='dispatched' AND (j.task_id IS NULL OR (t.assignee_agent_id=j.agent_id AND t.archived_at IS NULL)))`, run, user).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrSpaceForbidden
	}
	return nil
}
