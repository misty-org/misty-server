import { randomBytes } from "node:crypto";
import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import type { PasswordHasher } from "../auth/passwords.js";
import { hashToken } from "../auth/service.js";
import { SelfHostError } from "./repository.js";

export function createSelfHostAdmin(pool: Pool, passwords: PasswordHasher, clock = () => new Date()) {
  return {
    async bootstrapToken() {
      const token = randomBytes(32).toString("base64url"), now = clock(), expiresAt = new Date(now.getTime() + 1800000);
      await withTransaction(pool, async (tx) => {
        await tx.query("SELECT pg_advisory_xact_lock(hashtextextended('misty-self-host-bootstrap',0))");
        if ((await tx.query("SELECT 1 FROM self_host_accounts LIMIT 1")).rowCount) throw new SelfHostError("bootstrap_token_invalid");
        await tx.query("INSERT INTO self_host_bootstrap_tokens(token_hash,expires_at,created_at) VALUES($1,$2,$3)", [hashToken(token), expiresAt, now]);
      }, { mode: "service" });
      return { token, expiresAt };
    },
    async changeAccount(email: string, action: { kind: "disable" } | { kind: "password"; password: string }) {
      if (action.kind === "password" && (Buffer.byteLength(action.password, "utf8") < 8 || Buffer.byteLength(action.password, "utf8") > 72)) throw new Error("Password must contain 8 to 72 UTF-8 bytes");
      const passwordHash = action.kind === "password" ? await passwords.hash(action.password) : null;
      await withTransaction(pool, async (tx) => {
        const user = (await tx.query<{ id: string; email: string }>(`SELECT id,email FROM users WHERE LOWER(email)=$1
          AND EXISTS(SELECT 1 FROM self_host_accounts WHERE user_id=users.id) FOR UPDATE`, [email.trim().toLowerCase()])).rows[0];
        if (!user) throw new Error("Self-hosted account not found");
        if (action.kind === "disable") {
          await tx.query("UPDATE self_host_accounts SET disabled_at=$2,updated_at=$2 WHERE user_id=$1", [user.id, clock()]);
          await tx.query("UPDATE self_host_enrollment_invitations SET revoked_at=$2 WHERE created_by=$1 AND consumed_at IS NULL AND revoked_at IS NULL", [user.id, clock()]);
        } else await tx.query("UPDATE users SET password_hash=$2 WHERE id=$1", [user.id, passwordHash]);
        for (const table of ["sessions", "app_runtime_sessions", "auth_handoff_tokens", "password_reset_tokens"] as const) await tx.query(`DELETE FROM ${table} WHERE user_id=$1`, [user.id]);
        await tx.query(`UPDATE password_recovery_jobs SET state='superseded',lease_owner=NULL,lease_expires_at=NULL,updated_at=$2
          WHERE (issued_user_id=$1 OR email=$3) AND state IN ('pending','processing')`, [user.id, clock(), user.email.toLowerCase()]);
      }, { mode: "service" });
    },
  };
}
