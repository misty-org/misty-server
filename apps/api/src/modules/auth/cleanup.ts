import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";

/** Small independent batches avoid holding account locks or blocking login. */
export function createAuthCleanup(pool: Pool, now: () => Date = () => new Date()) {
  return {
    async runOnce() {
      let removed = 0;
      for (const table of ["sessions", "app_runtime_sessions", "password_reset_tokens", "auth_handoff_tokens", "password_recovery_jobs", "connection_authorization_requests", "connected_account_oauth_states"] as const) {
        const cutoff = new Date(now().getTime() - (table === "password_recovery_jobs" || table === "connection_authorization_requests" ? 86400_000 : 0));
        removed += await withTransaction(pool, async (tx) => (await tx.query(`WITH expired AS (
          SELECT ctid FROM ${table} WHERE expires_at<=$1 ORDER BY expires_at LIMIT 500 FOR UPDATE SKIP LOCKED)
          DELETE FROM ${table} WHERE ctid IN (SELECT ctid FROM expired)`, [cutoff])).rowCount ?? 0, { mode: "service" });
      }
      return removed > 0;
    },
  };
}
