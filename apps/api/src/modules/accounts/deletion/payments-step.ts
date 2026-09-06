import type { Pool } from "pg";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";
import { billingClosureResultSchema } from "../../../../../../packages/service-contracts/src/payments.js";
import type { BillingClosureClient } from "../../billing/closure-client.js";
import { createAccountDeletionJobs, type DeletionJob } from "./jobs.js";

/** No SQL locks span service I/O. This factory remains unmounted until the
 * provider, local and purge stages and legacy ownership handover are ready. */
export function createDeletionPaymentsWorker(options: { pool: Pool; billing: BillingClosureClient }) {
  const jobs = createAccountDeletionJobs(options.pool);
  async function acknowledge(job: DeletionJob, closed: boolean) {
    return withTransaction(options.pool, async tx => {
      await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
      const account = await tx.query(`SELECT id FROM users WHERE id=$1 AND license_id=$2 AND lifecycle_state='pending_deletion' FOR UPDATE`, [job.user_id, job.license_id]);
      if (!account.rowCount) return false;
      const parent = await tx.query(`SELECT id FROM account_deletion_requests WHERE id=$1 AND user_id=$2
        AND cleanup_owner='native' AND status='processing' FOR UPDATE`, [job.request_id, job.user_id]);
      if (!parent.rowCount) return false;
      const step = await tx.query(`SELECT request_id FROM account_deletion_steps WHERE request_id=$1 AND step='payments'
        AND state='processing' AND lease_token=$2 AND lease_expires_at>clock_timestamp() FOR UPDATE`, [job.request_id, job.lease_token]);
      if (!step.rowCount) return false;
      const changed = await tx.query(`UPDATE account_deletion_steps SET state=$3,lease_token=NULL,lease_expires_at=NULL,
        available_at=clock_timestamp()+interval '30 seconds',last_error_code='',updated_at=now(),
        completed_at=CASE WHEN $3='completed' THEN now() ELSE NULL END,
        result=CASE WHEN $3='completed' THEN '{"outcome":"billing_closed"}'::jsonb ELSE '{}'::jsonb END
        WHERE request_id=$1 AND step='payments' AND lease_token=$2 AND lease_expires_at>clock_timestamp()`,
      [job.request_id, job.lease_token, closed ? "completed" : "pending"]);
      if (!changed.rowCount) return false;
      // Do not erase a concurrent provider-stage diagnostic with a healthy poll.
      await tx.query(`UPDATE account_deletion_requests SET last_error_code=CASE WHEN last_error_code='payments_cleanup_unavailable' THEN '' ELSE last_error_code END,
        updated_at=now() WHERE id=$1`, [job.request_id]);
      return true;
    }, { mode: "service" });
  }
  return { async runOnce(signal: AbortSignal = new AbortController().signal): Promise<boolean> {
    signal.throwIfAborted();
    const job = await jobs.claim("payments"); if (!job) return false;
    try {
      signal.throwIfAborted();
      const result = billingClosureResultSchema.parse(await options.billing.close({ version: 1, userId: job.user_id,
        licenseId: job.license_id, deletionRequestId: job.request_id }, signal));
      signal.throwIfAborted();
      if (result.userId !== job.user_id || result.licenseId !== job.license_id || result.deletionRequestId !== job.request_id) throw new Error("Billing closure identity mismatch");
      await acknowledge(job, result.state === "closed");
    } catch { await jobs.retry(job); }
    return true;
  } };
}
