import { randomBytes, randomUUID } from "node:crypto";
import type { Pool } from "pg";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";
import type { PasswordHasher } from "../../auth/passwords.js";
import { hashToken } from "../../auth/service.js";
import { AccountUnavailable } from "../repository.js";
import { AccountReauthenticationFailed, verifyAccountPassword } from "../reauthentication.js";
import { lockDeletionAccount } from "./access.js";
import { disableAccount } from "./revocation.js";
import { AccountDeletionAlreadyPending, AccountDeletionNotFound, AccountDeletionOwnership, AccountDeletionUnavailable, deletionRequestColumns, deletionResponse, type DeletionRequest } from "./model.js";

/** Construction does not enable deletion. The production composition must only
 * mount this after all cleanup handlers and legacy ownership handover are ready. */
export function createAccountDeletion(options: { pool: Pool; passwords: PasswordHasher; deployment: "hosted" | "self_hosted" }) {
  let beginning = 0, reading = 0;
  return {
    async begin(userId: string, sessionHash: string, password: string, requestSignal: AbortSignal) {
      if (beginning >= 2) throw new AccountDeletionUnavailable();
      beginning++;
      const signal = AbortSignal.any([requestSignal, AbortSignal.timeout(25000)]);
      try {
        signal.throwIfAborted();
        const before = await withTransaction(options.pool, async tx => {
          await tx.query("SET LOCAL statement_timeout='5s'");
          const row = (await tx.query<{ password_hash: string }>(`SELECT u.password_hash FROM users u JOIN sessions s ON s.user_id=u.id
            WHERE u.id=$1 AND u.lifecycle_state='active' AND s.token_hash=$2 AND s.expires_at>clock_timestamp()`, [userId, sessionHash])).rows[0];
          if (!row) throw new AccountUnavailable(); return row;
        }, { mode: "service" });
        await verifyAccountPassword(options.passwords, password, before.password_hash, signal);
        return await withTransaction(options.pool, async tx => {
          await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
          signal.throwIfAborted();
          const current = await lockDeletionAccount(tx, userId, sessionHash);
          if (current.password_hash !== before.password_hash) throw new AccountReauthenticationFailed();
          const blockers = (await tx.query<{ space_id: string; name: string; member_count: number }>(`SELECT s.id AS space_id,s.name,COUNT(m.user_id)::integer AS member_count
            FROM spaces s JOIN space_members m ON m.space_id=s.id WHERE s.owner_user_id=$1 AND s.lifecycle_state='active'
            GROUP BY s.id,s.name HAVING COUNT(m.user_id)>1 ORDER BY s.name,s.id`, [userId])).rows;
          if (blockers.length) throw new AccountDeletionOwnership(blockers);
          signal.throwIfAborted();
          const requestId = `deletion_${randomUUID()}`, statusToken = randomBytes(32).toString("base64url");
          const request = (await tx.query<DeletionRequest>(`INSERT INTO account_deletion_requests(id,user_id,status_token_hash,purge_after,cleanup_owner)
            VALUES($1,$2,$3,now()+INTERVAL '30 days','native') RETURNING ${deletionRequestColumns}`, [requestId, userId, hashToken(statusToken)])).rows[0]!;
          await tx.query(`INSERT INTO account_deletion_steps(request_id,step,state,completed_at,result)
            SELECT $1,step,CASE WHEN step='payments' AND $2 THEN 'completed' ELSE 'pending' END,
              CASE WHEN step='payments' AND $2 THEN now() ELSE NULL END,
              CASE WHEN step='payments' AND $2 THEN '{"outcome":"not_applicable_self_hosted"}'::jsonb ELSE '{}'::jsonb END
            FROM unnest(ARRAY['payments','providers','local','purge']) AS step`, [requestId, options.deployment === "self_hosted"]);
          await disableAccount(tx, userId, current.email, current.spaces, signal);
          // The original session is now deleted in this transaction. Its row was
          // locked before deletion, so compare the captured exact expiry with DB
          // wall time to prevent a slow operation committing after authorization.
          if (!(await tx.query<{ valid: boolean }>("SELECT $1::timestamptz>clock_timestamp() AS valid", [current.sessionExpiresAt])).rows[0]!.valid) throw new AccountUnavailable();
          signal.throwIfAborted();
          return { request: deletionResponse(request), status_token: statusToken };
        }, { mode: "service" });
      } catch (error) {
        const pg = error as { code?: string; constraint?: string };
        if (pg?.code === "23505" && pg.constraint === "account_deletion_requests_active_user_idx") throw new AccountDeletionAlreadyPending();
        if (signal.aborted || ["55P03", "57014", "40P01", "40001"].includes(pg?.code ?? "")) throw new AccountDeletionUnavailable();
        throw error;
      } finally { beginning--; }
    },
    async status(requestId: string, statusToken: string, signal: AbortSignal) {
      if (reading >= 16) throw new AccountDeletionUnavailable();
      reading++;
      try {
        signal.throwIfAborted();
        return await withTransaction(options.pool, async tx => {
          await tx.query("SET LOCAL statement_timeout='5s'");
          const request = (await tx.query<DeletionRequest>(`SELECT ${deletionRequestColumns} FROM account_deletion_requests
            WHERE id=$1 AND status_token_hash=$2`, [requestId.trim(), hashToken(statusToken.trim())])).rows[0];
          signal.throwIfAborted();
          if (!request) throw new AccountDeletionNotFound(); return deletionResponse(request);
        }, { mode: "service" });
      } catch (error) {
        if (signal.aborted || error && typeof error === "object" && "code" in error && ["55P03", "57014"].includes(String(error.code))) throw new AccountDeletionUnavailable();
        throw error;
      } finally { reading--; }
    },
  };
}
