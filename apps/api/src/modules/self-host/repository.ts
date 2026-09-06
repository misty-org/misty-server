import { randomUUID } from "node:crypto";
import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import type { AuthUser } from "../auth/model.js";

export class SelfHostError extends Error {
  constructor(readonly code: "bootstrap_token_invalid" | "enrollment_invitation_invalid" | "entitlement_subject_already_enrolled" | "account_already_exists" | "admin_required" | "enrollment_invitation_not_found" | "entitlement_subject_mismatch" | "self_host_entitlement_required") { super(code); }
}
export type SelfHostAccess = { entitlement_subject: string; entitlement_expires_at: Date; is_admin: boolean; disabled_at: Date | null };
export function createSelfHostRepository(pool: Pool, clock = () => new Date()) {
  async function admin(tx: PoolClient, userId: string) {
    const user = await tx.query("SELECT id FROM users WHERE id=$1 AND lifecycle_state='active' FOR SHARE", [userId]);
    const access = (await tx.query<SelfHostAccess>("SELECT * FROM self_host_accounts WHERE user_id=$1 FOR SHARE", [userId])).rows[0];
    if (!user.rowCount || !access?.is_admin || access.disabled_at) throw new SelfHostError("admin_required");
    if (access.entitlement_expires_at <= clock()) throw new SelfHostError("self_host_entitlement_required");
  }
  return {
    async instance(name: string) {
      return withTransaction(pool, async (tx) => {
        await tx.query(`INSERT INTO misty_instance(singleton,server_id,display_name) VALUES(true,$1,$2)
          ON CONFLICT(singleton) DO UPDATE SET display_name=EXCLUDED.display_name,updated_at=now()`, [`server_${randomUUID()}`, name]);
        return (await tx.query<{ server_id: string; name: string; bootstrap_required: boolean }>(`SELECT server_id,display_name AS name,
          NOT EXISTS(SELECT 1 FROM self_host_accounts) AS bootstrap_required FROM misty_instance WHERE singleton=true`)).rows[0]!;
      }, { mode: "service" });
    },
    async access(userId: string) {
      return withTransaction(pool, async (tx) => (await tx.query<SelfHostAccess>("SELECT entitlement_subject,entitlement_expires_at,is_admin,disabled_at FROM self_host_accounts WHERE user_id=$1", [userId])).rows[0] ?? null, { mode: "user", userId });
    },
    async createAccount(input: { name: string; username: string; email: string; passwordHash: string; credentialHash: string; kind: "bootstrap" | "enroll";
      subject: string; proofExpiresAt: Date; sessionHash: string; sessionExpiresAt: Date }) {
      try {
        return await withTransaction(pool, async (tx) => {
          if (input.kind === "bootstrap") {
            await tx.query("SELECT pg_advisory_xact_lock(hashtextextended('misty-self-host-bootstrap',0))");
            const existing = await tx.query("SELECT 1 FROM self_host_accounts LIMIT 1");
            const authorized = await tx.query("SELECT token_hash FROM self_host_bootstrap_tokens WHERE token_hash=$1 AND consumed_at IS NULL AND expires_at>$2 FOR UPDATE", [input.credentialHash, clock()]);
            if (existing.rowCount || !authorized.rowCount) throw new SelfHostError("bootstrap_token_invalid");
          } else {
            const authorized = await tx.query("SELECT id FROM self_host_enrollment_invitations WHERE token_hash=$1 AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>$2 FOR UPDATE", [input.credentialHash, clock()]);
            if (!authorized.rowCount) throw new SelfHostError("enrollment_invitation_invalid");
          }
          if (input.proofExpiresAt <= clock()) throw new SelfHostError("self_host_entitlement_required");
          const userId = randomUUID(), licenseId = randomUUID();
          await tx.query(`SELECT set_config('app.rls_mode','registration',true),set_config('app.current_user_id',$1,true),
            set_config('app.current_license_id',$2,true),set_config('app.current_email',$3,true)`, [userId, licenseId, input.email]);
          const user = (await tx.query<AuthUser>(`INSERT INTO users(id,license_id,name,username,email,password_hash) VALUES($1,$2,$3,$4,$5,$6)
            RETURNING id,license_id,name,username,email`, [userId, licenseId, input.name, input.username, input.email, input.passwordHash])).rows[0]!;
          await tx.query("INSERT INTO licenses(id,user_id,tier,status,license_device) VALUES($1,$2,'basic','active','')", [user.license_id, user.id]);
          await tx.query("SELECT set_config('app.rls_mode','service',true)");
          await tx.query("INSERT INTO self_host_accounts(user_id,entitlement_subject,entitlement_expires_at,is_admin) VALUES($1,$2,$3,$4)", [user.id, input.subject, input.proofExpiresAt, input.kind === "bootstrap"]);
          if (input.kind === "bootstrap") await tx.query("UPDATE self_host_bootstrap_tokens SET consumed_at=$2 WHERE token_hash=$1", [input.credentialHash, clock()]);
          else await tx.query("UPDATE self_host_enrollment_invitations SET consumed_at=$2,consumed_by=$3 WHERE token_hash=$1", [input.credentialHash, clock(), user.id]);
          await tx.query("INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)", [input.sessionHash, user.id, input.sessionExpiresAt]);
          return user;
        }, { mode: "service" });
      } catch (error) {
        const pg = error as { code?: string; constraint?: string };
        if (pg.code === "23505") {
          if (pg.constraint === "self_host_accounts_entitlement_subject_key") throw new SelfHostError("entitlement_subject_already_enrolled");
          if (pg.constraint?.includes("email") || pg.constraint?.includes("username")) throw new SelfHostError("account_already_exists");
        }
        throw error;
      }
    },
    async invite(userId: string, id: string, hash: string, expiresAt: Date) {
      await withTransaction(pool, async (tx) => {
        await admin(tx, userId);
        await tx.query("INSERT INTO self_host_enrollment_invitations(id,token_hash,created_by,expires_at,created_at) VALUES($1,$2,$3,$4,$5)", [id, hash, userId, expiresAt, clock()]);
      }, { mode: "user", userId });
    },
    async revoke(userId: string, id: string) {
      await withTransaction(pool, async (tx) => {
        await admin(tx, userId);
        const revoked = await tx.query("UPDATE self_host_enrollment_invitations SET revoked_at=$3 WHERE id=$1 AND created_by=$2 AND consumed_at IS NULL AND revoked_at IS NULL", [id, userId, clock()]);
        if (!revoked.rowCount) throw new SelfHostError("enrollment_invitation_not_found");
      }, { mode: "user", userId });
    },
    async renew(userId: string, subject: string, expiresAt: Date) {
      await withTransaction(pool, async (tx) => {
        const updated = await tx.query("UPDATE self_host_accounts SET entitlement_expires_at=$3,updated_at=$4 WHERE user_id=$1 AND entitlement_subject=$2 AND disabled_at IS NULL", [userId, subject, expiresAt, clock()]);
        if (!updated.rowCount) throw new SelfHostError("entitlement_subject_mismatch");
      }, { mode: "user", userId });
    },
  };
}
export type SelfHostRepository = ReturnType<typeof createSelfHostRepository>;
