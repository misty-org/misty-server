import { randomUUID } from "node:crypto";
import type { Pool } from "pg";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";
import { EmailDeliveryError, type EmailSender } from "../../../../../../packages/email/src/mailjet.js";
import type { Logger } from "../../../../../../packages/runtime/src/logger.js";
import { hashToken } from "../service.js";
import type { RecoveryConfig } from "./config.js";
import { recoveryToken, type RecoveryTokenKeys } from "./token-keys.js";

type Job = { id: string; email: string; token_key_id: string; issued_at: Date | null; expires_at: Date; attempts: number; lease_owner: string };
const escapeHtml = (value: string) => value.replace(/[&<>"']/g, (character) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[character]!);
export function createRecoveryJobs(options: { pool: Pool; keys: RecoveryTokenKeys; send: EmailSender; config: RecoveryConfig; logger: Logger; now?: () => Date }) {
  const clock = options.now ?? (() => new Date());
  return {
    async enqueue(email: string) {
      const now = clock();
      await withTransaction(options.pool, (tx) => tx.query(`INSERT INTO password_recovery_jobs(id,email,token_key_id,expires_at,available_at,created_at)
        VALUES($1,$2,$3,$4,$5,$5)`, [randomUUID(), email, options.keys.active, new Date(now.getTime() + 900000), now]), { mode: "service" });
    },
    async runOnce() {
      const now = clock(), owner = randomUUID();
      const job = await withTransaction(options.pool, async (tx) => (await tx.query<Job>(`WITH pending AS MATERIALIZED (
          SELECT id,request_order FROM password_recovery_jobs WHERE state='pending' AND available_at<=$1
          ORDER BY available_at,created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED), stale AS MATERIALIZED (
          SELECT id,request_order FROM password_recovery_jobs WHERE state='processing' AND lease_expires_at<=$1
          ORDER BY lease_expires_at LIMIT 1 FOR UPDATE SKIP LOCKED), candidate AS (
          SELECT id FROM (SELECT * FROM pending UNION ALL SELECT * FROM stale) candidates ORDER BY request_order LIMIT 1)
        UPDATE password_recovery_jobs j SET state='processing',lease_owner=$2,lease_expires_at=$1::timestamptz+interval '60 seconds',attempts=j.attempts+1,updated_at=$1
        FROM candidate WHERE j.id=candidate.id RETURNING j.*`, [now, owner])).rows[0], { mode: "service" });
      if (!job) return false;
      try {
        const token = recoveryToken(options.keys, job.token_key_id, job.id, job.email);
        const ready = await withTransaction(options.pool, async (tx) => {
          // Account before job/token rows, matching reset's lock order.
          const user = (await tx.query<{ id: string; email: string }>("SELECT id,email FROM users WHERE LOWER(email)=$1 AND lifecycle_state='active' FOR UPDATE", [job.email])).rows[0];
          const current = (await tx.query<Job>("SELECT * FROM password_recovery_jobs WHERE id=$1 AND state='processing' AND lease_owner=$2 AND lease_expires_at>$3 FOR UPDATE", [job.id, owner, clock()])).rows[0];
          if (!current) return null;
          const newer = await tx.query("SELECT 1 FROM password_recovery_jobs WHERE email=$1 AND request_order>(SELECT request_order FROM password_recovery_jobs WHERE id=$2) LIMIT 1", [job.email, job.id]);
          if (!user || newer.rowCount || current.expires_at <= clock()) {
            await tx.query("UPDATE password_recovery_jobs SET state=$2,lease_owner=NULL,lease_expires_at=NULL,updated_at=$3 WHERE id=$1", [job.id, current.expires_at <= clock() ? "expired" : "superseded", clock()]);
            return null;
          }
          if (current.issued_at) {
            const live = await tx.query("SELECT 1 FROM password_reset_tokens WHERE user_id=$1 AND hashed_token=$2 AND expires_at>$3", [user.id, hashToken(token), clock()]);
            if (!live.rowCount) {
              await tx.query("UPDATE password_recovery_jobs SET state='superseded',lease_owner=NULL,lease_expires_at=NULL,updated_at=$2 WHERE id=$1", [job.id, clock()]);
              return null;
            }
          } else {
            await tx.query(`INSERT INTO password_reset_tokens(user_id,hashed_token,expires_at,created_at) VALUES($1,$2,$3,$4)
              ON CONFLICT(user_id) DO UPDATE SET hashed_token=EXCLUDED.hashed_token,expires_at=EXCLUDED.expires_at,created_at=EXCLUDED.created_at`, [user.id, hashToken(token), current.expires_at, clock()]);
            await tx.query("UPDATE password_recovery_jobs SET issued_user_id=$2,issued_at=$3 WHERE id=$1", [job.id, user.id, clock()]);
          }
          return user.email;
        }, { mode: "service" });
        if (!ready) return true;
        const link = new URL(options.config.startUrl); link.searchParams.set("token", token);
        await options.send({ to: ready, subject: "Reset your Misty password",
          text: `Reset your Misty password:\n\n${link.href}\n\nThis link expires 15 minutes after your request. If you did not request it, you can ignore this email.`,
          html: `<p><a href="${escapeHtml(link.href)}">Reset your Misty password</a></p><p>This link expires 15 minutes after your request. If you did not request it, you can ignore this email.</p>` });
        await withTransaction(options.pool, (tx) => tx.query("UPDATE password_recovery_jobs SET state='sent',lease_owner=NULL,lease_expires_at=NULL,updated_at=$3 WHERE id=$1 AND state='processing' AND lease_owner=$2", [job.id, owner, clock()]), { mode: "service" });
      } catch (error) {
        options.logger.warn({ jobId: job.id, reason: error instanceof EmailDeliveryError ? error.reason : "internal" }, "password recovery job failed");
        const retry = new Date(clock().getTime() + Math.min(60000, 1000 * 2 ** Math.min(job.attempts, 6)));
        await withTransaction(options.pool, (tx) => tx.query(`UPDATE password_recovery_jobs SET state=CASE WHEN expires_at<=$3 THEN 'expired' ELSE 'pending' END,
          available_at=$4,lease_owner=NULL,lease_expires_at=NULL,updated_at=$3 WHERE id=$1 AND state='processing' AND lease_owner=$2`, [job.id, owner, clock(), retry]), { mode: "service" });
      }
      return true;
    },
  };
}
export type RecoveryJobs = ReturnType<typeof createRecoveryJobs>;
