import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../../../packages/database/src/transaction.js";
import type { DeletionJob } from "../jobs.js";

/** Internal SQL/receipt constants only. A phase receipt never completes an account. */
export async function runPurgePhase(options: { pool: Pool; job: DeletionJob; spacesQuery: string; receipt: Record<string, string>;
  signal?: AbortSignal; erase: (tx: PoolClient, signal: AbortSignal) => Promise<void> }): Promise<boolean> {
  const { pool, job } = options;
  if (job.step !== "purge") return false;
  const signal = AbortSignal.any([options.signal ?? new AbortController().signal, AbortSignal.timeout(25_000)]);
  return withTransaction(pool, async tx => {
    signal.throwIfAborted();
    await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
    const spaces = (await tx.query<{ id: string }>(`SELECT id FROM spaces WHERE id IN (${options.spacesQuery}) ORDER BY id FOR UPDATE`, [job.user_id])).rows.map(row => row.id);
    if (!(await tx.query("SELECT id FROM users WHERE id=$1 AND license_id=$2 AND lifecycle_state='pending_deletion' FOR UPDATE", [job.user_id, job.license_id])).rowCount) return false;
    if ((await tx.query(`SELECT id FROM spaces WHERE id IN (${options.spacesQuery}) AND NOT(id=ANY($2::text[])) LIMIT 1`, [job.user_id, spaces])).rowCount) throw new Error("Account purge Space inventory changed");
    if (!(await tx.query(`SELECT id FROM account_deletion_requests WHERE id=$1 AND user_id=$2 AND cleanup_owner='native'
      AND status='scheduled' AND purge_after<=clock_timestamp() FOR UPDATE`, [job.request_id, job.user_id])).rowCount) return false;
    if (!(await tx.query(`SELECT request_id FROM account_deletion_steps WHERE request_id=$1 AND step='purge' AND state='processing'
      AND lease_token=$2 AND lease_expires_at>clock_timestamp() FOR UPDATE`, [job.request_id, job.lease_token])).rowCount) return false;
    if ((await tx.query(`SELECT step FROM account_deletion_steps WHERE request_id=$1 AND step IN ('payments','providers','local')
      AND state='completed' FOR SHARE`, [job.request_id])).rowCount !== 3) return false;
    await options.erase(tx, signal);
    signal.throwIfAborted();
    const changed = await tx.query(`UPDATE account_deletion_steps SET state='pending',lease_token=NULL,lease_expires_at=NULL,
      available_at=clock_timestamp()+interval '30 seconds',updated_at=now(),last_error_code='',result=result||$3::jsonb
      WHERE request_id=$1 AND step='purge' AND state='processing' AND lease_token=$2 AND lease_expires_at>clock_timestamp()`,
    [job.request_id, job.lease_token, JSON.stringify(options.receipt)]);
    if (!changed.rowCount) throw new Error("Account purge lease expired");
    signal.throwIfAborted(); return true;
  }, { mode: "service" });
}
