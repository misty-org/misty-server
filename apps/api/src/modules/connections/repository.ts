import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import type { SpaceActor } from "../spaces/access.js";
import { requireConnectionActor } from "./access.js";
import { SpaceError } from "../spaces/model.js";
import type { ConnectionRevoker, RevocationCredential, RevocationResult } from "./revocation.js";

export class ConnectionUnavailable extends Error {}
export function createConnectionRepository(pool: Pool, revoke: ConnectionRevoker | null) {
  return {
    list: (actor: SpaceActor) => withTransaction(pool, async (tx) => {
      await requireConnectionActor(tx, actor, "connections.read");
      const rows = (await tx.query(`SELECT id,provider,account_id,account_display,capabilities,granted_scopes,status,last_error_code,expires_at
        FROM connected_accounts WHERE user_id=$1 AND revoked_at IS NULL ORDER BY provider,account_display,id`, [actor.userId])).rows;
      return rows.map((row) => {
        if (!row.last_error_code) delete row.last_error_code;
        if (!row.expires_at) delete row.expires_at;
        return row;
      });
    }, { mode: "service" }),
    remove: (actor: SpaceActor, connectionId: string, requestSignal: AbortSignal) => withTransaction(pool, async (tx) => {
      await requireConnectionActor(tx, actor, "connections.write");
      const row = (await tx.query<RevocationCredential>(`SELECT provider,credential_ciphertext,credential_nonce,key_version
        FROM connected_accounts WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL FOR UPDATE`, [connectionId, actor.userId])).rows[0];
      if (!row) throw new SpaceError("not_found");
      if (!revoke) throw new ConnectionUnavailable();
      const hooks = row.provider === "figma" ? (await tx.query<{ webhook_id: string }>(`SELECT h.webhook_id FROM figma_webhook_subscriptions h
        JOIN figma_space_bindings b ON b.id=h.binding_id WHERE b.connection_id=$1 AND b.bound_by_user_id=$2 AND b.disabled_at IS NULL AND h.status<>'disabled'
        ORDER BY h.id`, [connectionId, actor.userId])).rows.map((hook) => hook.webhook_id) : [];
      // Complete local SQL before external effects. Failure here rolls everything
      // back without invoking the provider. Keep the connection locked through
      // the bounded remote attempt so refresh/upsert cannot replace its credential.
      if (row.provider === "figma") {
        await tx.query(`UPDATE figma_webhook_subscriptions SET status='disabled',updated_at=now() WHERE binding_id IN
          (SELECT id FROM figma_space_bindings WHERE connection_id=$1 AND bound_by_user_id=$2)`, [connectionId, actor.userId]);
        await tx.query(`UPDATE provider_shared_resources SET status='disabled',updated_at=now() WHERE id IN
          (SELECT shared_resource_id FROM figma_space_bindings WHERE connection_id=$1 AND bound_by_user_id=$2)`, [connectionId, actor.userId]);
        await tx.query(`UPDATE space_integrations SET status='disabled',updated_at=now() WHERE id IN
          (SELECT integration_id FROM figma_space_bindings WHERE connection_id=$1 AND bound_by_user_id=$2)`, [connectionId, actor.userId]);
        await tx.query(`UPDATE figma_space_bindings SET status='disabled',disabled_at=now(),updated_at=now()
          WHERE connection_id=$1 AND bound_by_user_id=$2 AND disabled_at IS NULL`, [connectionId, actor.userId]);
      }
      await tx.query(`UPDATE connected_accounts SET credential_ciphertext=''::bytea,credential_nonce=''::bytea,status='revoked',last_error_code='',revoked_at=now(),updated_at=now()
        WHERE id=$1 AND user_id=$2`, [connectionId, actor.userId]);
      const signal = AbortSignal.any([requestSignal, AbortSignal.timeout(15000)]);
      let result: RevocationResult;
      try { result = await revoke(row, hooks, signal); }
      catch { result = "provider_revocation_failed_local_credentials_erased"; }
      finally { row.credential_ciphertext.fill(0); row.credential_nonce.fill(0); }
      return result;
    }, { mode: "service" }),
  };
}
