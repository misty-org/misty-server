import type { PoolClient } from "pg";

/** Caller holds the account write lock after affected Space locks. Cancellation
 * denies future authorization; it does not assert that an external effect stopped. */
export async function cancelAccountAi(tx: PoolClient, userId: string, signal: AbortSignal) {
  const query = async (sql: string) => { signal.throwIfAborted(); return tx.query(sql, [userId]); };
  await query(`INSERT INTO ai_user_settings(user_id,enabled,cursor_companion_enabled,memory_enabled,disabled_at)
    VALUES($1,false,false,false,now()) ON CONFLICT(user_id) DO UPDATE SET enabled=false,cursor_companion_enabled=false,
    memory_enabled=false,disabled_at=COALESCE(ai_user_settings.disabled_at,now()),updated_at=now()`);
  await query("UPDATE ai_surface_preferences SET proactive_enabled=false,updated_at=now() WHERE user_id=$1");
  await query(`UPDATE ai_recaps SET enabled=false,next_run_at=NULL,
    state=CASE WHEN state='running' THEN 'failed' ELSE state END,
    last_error=CASE WHEN state='running' THEN 'account_disabled' ELSE last_error END,updated_at=now() WHERE user_id=$1`);
  // Preserve runtime identity, payload and heartbeat for cancellation/billing
  // reconciliation. Existing terminal results must not be overwritten.
  await query(`UPDATE ai_invocations SET state='canceled',error_code='account_disabled',
    canceled_at=COALESCE(canceled_at,now()),updated_at=now() WHERE user_id=$1 AND state IN ('queued','running','awaiting_approval')`);
  await query("UPDATE ai_invocation_contexts SET state='detached',updated_at=now() WHERE user_id=$1 AND state='attached'");
  // Applying actions may have reached a provider. Retain their unresolved state
  // and the toolbox journal; retention purge deliberately waits for reconciliation.
  await query(`UPDATE ai_artifacts SET state='rejected',error_message='account_disabled',decided_at=now(),updated_at=now()
    WHERE user_id=$1 AND state='proposed'`);
}
