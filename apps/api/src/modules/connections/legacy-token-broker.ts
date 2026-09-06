import { createHash } from "node:crypto";
import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { requireActiveAccount, requireSpaceActor, type SpaceActor } from "../spaces/access.js";
import { SpaceError } from "../spaces/model.js";
import type { ConnectionCipher } from "./credentials.js";
import { providerTokenSchema, TokenExchangeError, type TokenRefresher } from "./oauth-token.js";
import { ProviderRequestError as CalendarProviderError } from "./provider-error.js";

type Credential = { id: string; user_id: string; ciphertext: Buffer; nonce: Buffer; key_version: number; expires_at: Date | null };
export type LegacyTokenLease = { id: string; userId: string; spaceId: string; integrationId: string; fingerprint: string; accessToken: string; tokenType: string };
const fingerprint = (row: Credential) => createHash("sha256").update(String(row.key_version)).update(row.nonce).update(row.ciphertext).digest("hex");
export function createLegacyTokenBroker(options: { pool: Pool; cipher: ConnectionCipher; refresh: TokenRefresher }) {
  const rowFor = async (tx: PoolClient, spaceId: string, integrationId: string, userId: string, write = false) => {
    const row = (await tx.query<Credential>(`SELECT c.id,c.user_id,c.ciphertext,c.nonce,c.key_version,c.expires_at
      FROM space_provider_credentials c JOIN space_integrations i ON i.id=c.integration_id AND i.space_id=c.space_id
      WHERE c.integration_id=$1 AND c.space_id=$2 AND c.provider='google' AND i.provider='google' AND c.revoked_at IS NULL AND i.status='active'
      AND (c.user_id=$3 OR EXISTS(SELECT 1 FROM spaces s WHERE s.id=$2 AND s.owner_user_id=$3)
        OR EXISTS(SELECT 1 FROM provider_shared_resources r WHERE r.integration_id=c.integration_id AND r.space_id=$2 AND r.status='active'))
      FOR ${write ? "UPDATE" : "SHARE"} OF c,i`, [integrationId, spaceId, userId])).rows[0];
    if (!row) throw new SpaceError("not_found");
    return row;
  };
  return {
    async acquire(actor: SpaceActor, spaceId: string, integrationId: string, signal: AbortSignal, appScope = "calendar.read"): Promise<LegacyTokenLease> {
      return withTransaction(options.pool, async tx => {
        await tx.query("SET LOCAL lock_timeout='2s'");
        await requireSpaceActor(tx, actor, spaceId, false, appScope);
        const row = await rowFor(tx, spaceId, integrationId, actor.userId, true);
        try {
          await requireActiveAccount(tx, row.user_id);
          let token;
          try {
            const plain = options.cipher.decryptLegacy("google", row.ciphertext, row.nonce, row.key_version);
            try { token = providerTokenSchema.parse(JSON.parse(plain.toString())); } finally { plain.fill(0); }
          } catch { throw new CalendarProviderError(); }
          if (row.expires_at && row.expires_at.getTime() < Date.now() + 300000) {
            if (!token.refresh_token) throw new CalendarProviderError(401);
            let refreshed;
            try { refreshed = providerTokenSchema.parse(await options.refresh("google", token.refresh_token, signal)); }
            catch (error) { throw new CalendarProviderError(error instanceof TokenExchangeError && error.code === "reauthorization_required" ? 401 : 0); }
            if (!refreshed.expires_in) throw new CalendarProviderError();
            if (!refreshed.refresh_token) refreshed.refresh_token = token.refresh_token;
            const plain = Buffer.from(JSON.stringify(refreshed));
            let sealed;
            try { sealed = options.cipher.encryptLegacy("google", plain); } finally { plain.fill(0); }
            row.ciphertext.fill(0); row.nonce.fill(0);
            row.ciphertext = sealed.ciphertext; row.nonce = sealed.nonce; row.key_version = sealed.keyVersion;
            await tx.query(`UPDATE space_provider_credentials SET ciphertext=$2,nonce=$3,key_version=$4,expires_at=$5,last_refreshed_at=now(),updated_at=now() WHERE id=$1`,
              [row.id, row.ciphertext, row.nonce, row.key_version, new Date(Date.now() + refreshed.expires_in * 1000)]);
            token = refreshed;
          }
          return { id: row.id, userId: actor.userId, spaceId, integrationId, fingerprint: fingerprint(row), accessToken: token.access_token, tokenType: token.token_type || "Bearer" };
        } finally { row.ciphertext.fill(0); row.nonce.fill(0); }
      }, { mode: "service" });
    },
    async assertCurrent(tx: PoolClient, lease: LegacyTokenLease) {
      await requireSpaceActor(tx, { userId: lease.userId }, lease.spaceId);
      const row = await rowFor(tx, lease.spaceId, lease.integrationId, lease.userId);
      try {
        await requireActiveAccount(tx, row.user_id);
        if (row.id !== lease.id || fingerprint(row) !== lease.fingerprint) throw new SpaceError("forbidden");
      } finally { row.ciphertext.fill(0); row.nonce.fill(0); }
    },
  };
}
