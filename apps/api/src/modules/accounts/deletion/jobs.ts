import { randomUUID } from "node:crypto";
import type { Pool } from "pg";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";

export type DeletionStep = "payments" | "providers" | "local" | "purge";
export type DeletionJob = { request_id: string; step: DeletionStep; user_id: string; license_id: string; lease_token: string; attempts: number };

/** Only explicit native requests are eligible. Claiming does not perform cleanup
 * or mark a stage complete; each handler must persist its effect with a matching,
 * unexpired lease before advancing the request. No production worker is mounted
 * until all four effects and legacy-owner handover are implemented. */
export function createAccountDeletionJobs(pool: Pool) {
  return {
    async claim(step: DeletionStep): Promise<DeletionJob | null> {
      return withTransaction(pool, async tx => {
        await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
        const result = await tx.query<DeletionJob>(`WITH candidate AS (
          SELECT s.request_id,s.step FROM account_deletion_steps s
          JOIN account_deletion_requests r ON r.id=s.request_id JOIN users u ON u.id=r.user_id
          WHERE s.step=$1 AND r.cleanup_owner='native' AND u.lifecycle_state='pending_deletion'
            AND ((s.state='pending' AND s.available_at<=clock_timestamp()) OR (s.state='processing' AND s.lease_expires_at<=clock_timestamp()))
            AND ((s.step IN ('payments','providers') AND r.status='processing')
              OR (s.step='local' AND r.status='processing'
                AND EXISTS(SELECT 1 FROM account_deletion_steps p WHERE p.request_id=s.request_id AND p.step='payments' AND p.state='completed')
                AND EXISTS(SELECT 1 FROM account_deletion_steps p WHERE p.request_id=s.request_id AND p.step='providers' AND p.state='completed'))
              OR (s.step='purge' AND r.status='scheduled' AND r.purge_after<=clock_timestamp()
                AND EXISTS(SELECT 1 FROM account_deletion_steps p WHERE p.request_id=s.request_id AND p.step='local' AND p.state='completed')))
          ORDER BY s.available_at,s.created_at,s.request_id FOR UPDATE OF s SKIP LOCKED LIMIT 1
        ), claimed AS (
          UPDATE account_deletion_steps s SET state='processing',lease_token=$2,lease_expires_at=clock_timestamp()+interval '2 minutes',
            attempts=s.attempts+1,updated_at=now() FROM candidate c WHERE s.request_id=c.request_id AND s.step=c.step
          RETURNING s.request_id,s.step,s.lease_token,s.attempts
        ) SELECT c.*,r.user_id,u.license_id FROM claimed c JOIN account_deletion_requests r ON r.id=c.request_id JOIN users u ON u.id=r.user_id`, [step, randomUUID()]);
        return result.rows[0] ?? null;
      }, { mode: "service" });
    },
    async retry(job: DeletionJob): Promise<boolean> {
      return withTransaction(pool, async tx => {
        await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
        // Parent before step, matching the completion transaction's lock order.
        const parent = await tx.query(`SELECT id FROM account_deletion_requests WHERE id=$1 AND user_id=$2
          AND cleanup_owner='native' AND status IN ('processing','scheduled') FOR UPDATE`, [job.request_id, job.user_id]);
        if (!parent.rowCount) return false;
        // Store only a module-owned error code, never provider errors or payloads.
        const code = `${job.step}_cleanup_unavailable`;
        const changed = await tx.query(`UPDATE account_deletion_steps SET state='pending',lease_token=NULL,lease_expires_at=NULL,
          available_at=clock_timestamp()+make_interval(secs=>LEAST(3600,15*power(2,LEAST(attempts-1,8)))::integer),last_error_code=$4,updated_at=now()
          WHERE request_id=$1 AND step=$2 AND state='processing' AND lease_token=$3 AND lease_expires_at>clock_timestamp()`, [job.request_id, job.step, job.lease_token, code]);
        if (!changed.rowCount) return false;
        await tx.query("UPDATE account_deletion_requests SET last_error_code=$2,updated_at=now() WHERE id=$1", [job.request_id, code]);
        return true;
      }, { mode: "service" });
    },
  };
}
