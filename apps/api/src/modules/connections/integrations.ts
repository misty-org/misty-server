import { randomUUID } from "node:crypto";
import type { Pool } from "pg";
import { z } from "zod";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { requireSpaceActor, type SpaceActor } from "../spaces/access.js";
import { spacePermissions } from "../spaces/permissions.js";
import { SpaceError, trimSpace } from "../spaces/model.js";
import type { ConnectionCipher } from "./credentials.js";
import { ConnectionUnavailable } from "./repository.js";

const columns = "id,space_id,provider,display_name,granted_permissions,status,connected_by_user_id,created_at,updated_at";
type Account = { id: string; provider: string; status: string; capabilities: string[]; granted_scopes: string[]; account_id: string; account_display: string;
  credential_ciphertext: Buffer; credential_nonce: Buffer; key_version: number; expires_at: Date | null };
export function createIntegrationRepository(pool: Pool, cipher: ConnectionCipher | null) {
  return {
    list(actor: SpaceActor, spaceId: string) {
      return withTransaction(pool, async tx => {
        await requireSpaceActor(tx, actor, spaceId, false, "connections.read");
        return (await tx.query(`SELECT ${columns} FROM space_integrations WHERE space_id=$1 ORDER BY provider,display_name,id`, [spaceId])).rows;
      }, { mode: "service" });
    },
    bind(actor: SpaceActor, spaceId: string, rawProvider: string, raw: unknown) {
      const parsed = z.object({ connection_id: z.string().min(1), capability: z.string().nullable().optional().transform(value => value ?? "") }).safeParse(raw);
      if (!parsed.success) throw new SpaceError("invalid_request");
      const provider = trimSpace(rawProvider).toLowerCase(), capability = trimSpace(parsed.data.capability || "calendar_read").toLowerCase();
      return withTransaction(pool, async tx => {
        await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
        const member = await requireSpaceActor(tx, actor, spaceId, false, "connections.write");
        const permissions: Record<string, boolean> = await spacePermissions(tx, actor.userId, { id: spaceId, role: member.role, is_default: false });
        if (!permissions["integrations.manage"]) throw new SpaceError("forbidden");
        const account = (await tx.query<Account>(`SELECT id,provider,status,capabilities,granted_scopes,account_id,account_display,credential_ciphertext,credential_nonce,key_version,expires_at
          FROM connected_accounts WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL FOR SHARE`, [parsed.data.connection_id, actor.userId])).rows[0];
        if (!account) throw new SpaceError("not_found");
        try {
          if (provider !== "google" || account.provider !== provider || account.status !== "active" || !["calendar_read", "calendar_write"].includes(capability) || !account.capabilities.includes(capability)) throw new SpaceError("forbidden");
          if (!cipher) throw new ConnectionUnavailable();
          let plaintext: Buffer;
          try { plaintext = cipher.decrypt(provider, account.credential_ciphertext, account.credential_nonce, account.key_version); }
          catch { throw new SpaceError("invalid_request"); }
          let encrypted: ReturnType<ConnectionCipher["encryptLegacy"]>;
          try {
            let token: unknown;
            try { token = JSON.parse(plaintext.toString("utf8")); } catch { throw new SpaceError("invalid_request"); }
            if (!z.object({ access_token: z.string().refine(value => !!trimSpace(value)) }).safeParse(token).success) throw new SpaceError("invalid_request");
            encrypted = cipher.encryptLegacy(provider, plaintext);
          } finally { plaintext.fill(0); }
          try {
            const credentialId = `credential_${randomUUID()}`;
            const integration = (await tx.query<{ id: string } & Record<string, unknown>>(`INSERT INTO space_integrations(id,space_id,provider,display_name,credential_reference,granted_permissions,status,connected_by_user_id)
              VALUES($1,$2,$3,$4,$5,$6::jsonb,'active',$7) ON CONFLICT(space_id,connected_by_user_id,provider,display_name)
              DO UPDATE SET granted_permissions=EXCLUDED.granted_permissions,status='active',updated_at=now() RETURNING ${columns}`,
            [`integration_${randomUUID()}`, spaceId, provider, account.account_display, credentialId, JSON.stringify(account.granted_scopes), actor.userId])).rows[0]!;
            const credential = (await tx.query<{ id: string }>(`INSERT INTO space_provider_credentials(id,integration_id,space_id,user_id,provider,ciphertext,nonce,key_version,account_id,account_display,expires_at,last_refreshed_at)
              VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now()) ON CONFLICT(integration_id) DO UPDATE SET ciphertext=EXCLUDED.ciphertext,nonce=EXCLUDED.nonce,key_version=EXCLUDED.key_version,
              account_id=EXCLUDED.account_id,account_display=EXCLUDED.account_display,expires_at=EXCLUDED.expires_at,last_refreshed_at=now(),revoked_at=NULL,updated_at=now() RETURNING id`,
            [credentialId, integration.id, spaceId, actor.userId, provider, encrypted.ciphertext, encrypted.nonce, encrypted.keyVersion, account.account_id, account.account_display, account.expires_at])).rows[0]!;
            // Rebinding preserves the credential row's ID; keep the private reference valid.
            await tx.query("UPDATE space_integrations SET credential_reference=$2 WHERE id=$1 AND space_id=$3", [integration.id, credential.id, spaceId]);
            return { integration, connection_id: account.id, capability };
          } finally { encrypted.ciphertext.fill(0); encrypted.nonce.fill(0); }
        } finally { account.credential_ciphertext.fill(0); account.credential_nonce.fill(0); }
      }, { mode: "service" });
    },
  };
}
