import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { AccountConflict, AuthRejected, SelfHostSubjectMismatch, type AuthUser } from "./model.js";

const userColumns = "id,license_id,name,username,email";
export function createAuthRepository(pool: Pool) {
  return {
    async findByEmail(email: string) {
      return withTransaction(pool, async (tx) => (await tx.query<AuthUser & { password_hash: string }>(
        `SELECT ${userColumns},password_hash FROM users WHERE LOWER(email)=$1 AND lifecycle_state='active'`, [email])).rows[0] ?? null,
      { mode: "anonymous", email });
    },
    async register(input: { id: string; licenseId: string; name: string; username: string; email: string; passwordHash: string; tokenHash: string; expiresAt: Date; analyticsEnabled: boolean }) {
      try {
        return await withTransaction(pool, async (tx) => {
          const user = (await tx.query<AuthUser>(`INSERT INTO users(id,license_id,name,username,email,password_hash,analytics_enabled)
            VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING ${userColumns}`,
            [input.id, input.licenseId, input.name, input.username, input.email, input.passwordHash, input.analyticsEnabled])).rows[0]!;
          await tx.query("INSERT INTO licenses(id,user_id,tier,status,license_device) VALUES($1,$2,'basic','active','')", [input.licenseId, input.id]);
          await tx.query("SELECT set_config('app.rls_mode','session',true),set_config('app.current_session_token_hash',$1,true)", [input.tokenHash]);
          await tx.query("INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)", [input.tokenHash, input.id, input.expiresAt]);
          return user;
        }, { mode: "registration", userId: input.id, email: input.email, licenseId: input.licenseId });
      } catch (error) {
        const pg = error as { code?: string; constraint?: string };
        if (pg.code === "23505" && pg.constraint?.includes("username")) throw new AccountConflict("username");
        if (pg.code === "23505" && pg.constraint?.includes("email")) throw new AccountConflict("email");
        throw error;
      }
    },
    async createSession(input: { userId: string; expectedPasswordHash: string; tokenHash: string; expiresAt: Date;
      selfHostProof?: { subject: string; expiresAt: Date } }) {
      return withTransaction(pool, async (tx) => {
        // Serialize with password reset/deletion. Verifying a password outside
        // the transaction must not allow a stale hash to mint a fresh session.
        const user = (await tx.query<AuthUser>(`SELECT ${userColumns} FROM users
          WHERE id=$1 AND password_hash=$2 AND lifecycle_state='active' FOR SHARE`, [input.userId, input.expectedPasswordHash])).rows[0];
        if (!user) throw new AuthRejected("invalid credentials");
        if (input.selfHostProof) {
          const changed = await tx.query(`UPDATE self_host_accounts SET entitlement_expires_at=$3,updated_at=now()
            WHERE user_id=$1 AND entitlement_subject=$2 AND disabled_at IS NULL`, [input.userId, input.selfHostProof.subject, input.selfHostProof.expiresAt]);
          if (!changed.rowCount) throw new SelfHostSubjectMismatch("Self-host entitlement subject mismatch");
        }
        await tx.query("INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)", [input.tokenHash, input.userId, input.expiresAt]);
        return user;
      }, { mode: "session", sessionHash: input.tokenHash, userId: input.userId });
    },
    async findSession(tokenHash: string, now: Date) {
      return withTransaction(pool, async (tx) => {
        const session = (await tx.query<{ user_id: string }>("SELECT user_id FROM sessions WHERE token_hash=$1 AND expires_at>$2", [tokenHash, now])).rows[0];
        if (!session) return null;
        await tx.query("SELECT set_config('app.current_user_id',$1,true)", [session.user_id]);
        return (await tx.query<AuthUser>(`SELECT ${userColumns} FROM users WHERE id=$1 AND lifecycle_state='active'`, [session.user_id])).rows[0] ?? null;
      }, { mode: "session", sessionHash: tokenHash });
    },
    async deleteSession(tokenHash: string) {
      await withTransaction(pool, (tx) => tx.query("DELETE FROM sessions WHERE token_hash=$1", [tokenHash]), { mode: "session", sessionHash: tokenHash });
    },
  };
}
export type AuthRepository = ReturnType<typeof createAuthRepository>;
