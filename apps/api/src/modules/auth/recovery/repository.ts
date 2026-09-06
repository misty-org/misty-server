import type { Pool } from "pg";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";

export function createRecoveryRepository(pool: Pool) {
  return {
    async validate(hash: string, now: Date) {
      return withTransaction(pool, async (tx) => {
        const result = await tx.query(`SELECT 1 FROM password_reset_tokens t JOIN users u ON u.id=t.user_id
          WHERE t.hashed_token=$1 AND t.expires_at>$2 AND u.lifecycle_state='active'`, [hash, now]);
        return Boolean(result.rowCount);
      }, { mode: "service" });
    },
    async reset(hash: string, passwordHash: string, now: Date) {
      return withTransaction(pool, async (tx) => {
        const candidate = (await tx.query<{ user_id: string }>("SELECT user_id FROM password_reset_tokens WHERE hashed_token=$1", [hash])).rows[0];
        if (!candidate) return false;
        // Account before token/session locks, shared with login and handoff.
        // Re-read the exact token after the lock: another request may replace it.
        const user = await tx.query("SELECT id FROM users WHERE id=$1 AND lifecycle_state='active' FOR UPDATE", [candidate.user_id]);
        if (!user.rowCount) return false;
        const consumed = await tx.query("DELETE FROM password_reset_tokens WHERE user_id=$1 AND hashed_token=$2 AND expires_at>$3 RETURNING user_id", [candidate.user_id, hash, now]);
        if (!consumed.rowCount) return false;
        await tx.query("UPDATE users SET password_hash=$2 WHERE id=$1", [candidate.user_id, passwordHash]);
        await tx.query("DELETE FROM sessions WHERE user_id=$1", [candidate.user_id]);
        await tx.query("DELETE FROM app_runtime_sessions WHERE user_id=$1", [candidate.user_id]);
        await tx.query("DELETE FROM auth_handoff_tokens WHERE user_id=$1", [candidate.user_id]);
        await tx.query(`UPDATE password_recovery_jobs SET state='superseded',lease_owner=NULL,lease_expires_at=NULL,updated_at=$2
          WHERE (issued_user_id=$1 OR email=(SELECT LOWER(email) FROM users WHERE id=$1)) AND state IN ('pending','processing')`, [candidate.user_id, now]);
        return true;
      }, { mode: "service" });
    },
  };
}
export type RecoveryRepository = ReturnType<typeof createRecoveryRepository>;
