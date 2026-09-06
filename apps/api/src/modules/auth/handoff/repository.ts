import type { Pool } from "pg";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";

export function createHandoffRepository(pool: Pool) {
  return {
    async mint(input: { sourceSessionHash: string; tokenHash: string; redirectPath: string; now: Date; expiresAt: Date }) {
      return withTransaction(pool, async (tx) => {
        const session = (await tx.query<{ user_id: string }>(`SELECT user_id FROM sessions WHERE token_hash=$1 AND expires_at>$2`, [input.sourceSessionHash, input.now])).rows[0];
        if (!session) return false;
        // Account first, then credential rows: password reset/deletion uses the
        // same order. Recheck the session after acquiring the account lock.
        const user = await tx.query("SELECT id FROM users WHERE id=$1 AND lifecycle_state='active' FOR SHARE", [session.user_id]);
        if (!user.rowCount) return false;
        const active = await tx.query("SELECT user_id FROM sessions WHERE token_hash=$1 AND expires_at>$2 FOR SHARE", [input.sourceSessionHash, input.now]);
        if (!active.rowCount) return false;
        await tx.query(`INSERT INTO auth_handoff_tokens(hashed_token,user_id,redirect_path,expires_at) VALUES($1,$2,$3,$4)`,
          [input.tokenHash, session.user_id, input.redirectPath, input.expiresAt]);
        return true;
      }, { mode: "service" });
    },
    async redeem(input: { tokenHash: string; sessionHash: string; now: Date; sessionExpiresAt: Date }) {
      return withTransaction(pool, async (tx) => {
        const candidate = (await tx.query<{ user_id: string }>("SELECT user_id FROM auth_handoff_tokens WHERE hashed_token=$1", [input.tokenHash])).rows[0];
        if (!candidate) return null;
        const user = await tx.query("SELECT id FROM users WHERE id=$1 AND lifecycle_state='active' FOR SHARE", [candidate.user_id]);
        const token = (await tx.query<{ user_id: string; redirect_path: string; expires_at: Date }>(
          "DELETE FROM auth_handoff_tokens WHERE hashed_token=$1 RETURNING user_id,redirect_path,expires_at", [input.tokenHash])).rows[0];
        if (!token || !user.rowCount || token.expires_at <= input.now) return null;
        // Consumption and the replacement session commit together. A database
        // failure must not strand the user by burning the one-time link.
        await tx.query("INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)", [input.sessionHash, token.user_id, input.sessionExpiresAt]);
        return token.redirect_path;
      }, { mode: "service" });
    },
  };
}
export type HandoffRepository = ReturnType<typeof createHandoffRepository>;
