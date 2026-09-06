import { randomBytes, randomUUID } from "node:crypto";
import type { Pool, PoolClient } from "pg";
import { z } from "zod";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { hashToken } from "../auth/service.js";
import type { PasswordHasher } from "../auth/passwords.js";
import { SpaceError, trimSpace } from "../spaces/model.js";
import { LibraryReauthenticationFailed, LibraryReauthenticationRequired, requireLibraryPermission } from "./model.js";
export async function requireLibraryGrant(tx: PoolClient, userId: string, spaceId: string, scope: string, rawToken: string) {
  if (!scope) return;
  const token = trimSpace(rawToken);
  if (!token || !(await tx.query("SELECT id FROM library_reauthentication_grants WHERE user_id=$1 AND space_id=$2 AND scope=$3 AND token_hash=$4 AND expires_at>clock_timestamp() FOR SHARE", [userId, spaceId, scope, hashToken(token)])).rowCount) throw new LibraryReauthenticationRequired();
}
export function createLibraryReauthentication(pool: Pool, passwords: PasswordHasher) {
  return async (userId: string, spaceId: string, raw: unknown) => {
    const parsed = z.object({ password: z.string(), scope: z.string().transform(trimSpace).pipe(z.enum(["hidden", "recently_deleted", "bulk_export"])) }).safeParse(raw);
    if (!parsed.success) throw new SpaceError("invalid_request");
    const { password, scope } = parsed.data;
    const before = await withTransaction(pool, async tx => {
      await requireLibraryPermission(tx, userId, spaceId);
      return (await tx.query<{ password_hash: string }>("SELECT password_hash FROM users WHERE id=$1 AND lifecycle_state='active'", [userId])).rows[0]!;
    }, { mode: "service" });
    const valid = password.length > 0 && Buffer.byteLength(password) <= 1024 && await passwords.verify(password, before.password_hash);
    const token = randomBytes(32).toString("base64url");
    const expires = await withTransaction(pool, async tx => {
      await requireLibraryPermission(tx, userId, spaceId);
      const current = (await tx.query<{ password_hash: string }>("SELECT password_hash FROM users WHERE id=$1 AND lifecycle_state='active' FOR SHARE", [userId])).rows[0];
      const accepted = valid && current?.password_hash === before.password_hash;
      await tx.query("INSERT INTO space_library_audit_events(request_id,space_id,actor_user_id,action,target_kind,target_id,outcome,details) VALUES($1,$2,$3,$4,'reauthentication_grant','',$5,$6::jsonb)",
        [`req_${randomUUID()}`, spaceId, userId, accepted ? "library.sensitive.reauthenticated" : "library.sensitive.reauthentication_denied", accepted ? "success" : "denied", JSON.stringify({ scope })]);
      if (!accepted) return null;
      await tx.query("DELETE FROM library_reauthentication_grants WHERE expires_at<=now() OR (user_id=$1 AND space_id=$2 AND scope=$3)", [userId, spaceId, scope]);
      return (await tx.query<{ expires_at: Date }>("INSERT INTO library_reauthentication_grants(id,user_id,space_id,scope,token_hash,expires_at) VALUES($1,$2,$3,$4,$5,now()+interval '5 minutes') RETURNING expires_at", [`reauth_${randomUUID()}`, userId, spaceId, scope, hashToken(token)])).rows[0]!.expires_at;
    }, { mode: "service" });
    if (!expires) throw new LibraryReauthenticationFailed();
    return { token, scope, expires_at: expires };
  };
}
