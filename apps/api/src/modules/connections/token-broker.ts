import { createHash } from "node:crypto";
import type { Pool, PoolClient } from "pg";
import { IdentifierSchema } from "@misty/contracts";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import type { SpaceActor } from "../spaces/access.js";
import { SpaceError } from "../spaces/model.js";
import { requireConnectionActor } from "./access.js";
import type { ConnectionCipher } from "./credentials.js";
import type { ConnectionProvider } from "./config.js";
import { providerTokenSchema, TokenExchangeError, type ProviderToken, type TokenRefresher } from "./oauth-token.js";

const policies = {
  mailRead: { scope: "mail.read", capability: "mail" },
  mailWrite: { scope: "mail.write", capability: "mail" },
} as const;
export type ConnectionTokenPurpose = keyof typeof policies;
export class ConnectionTokenError extends Error {
  constructor(readonly code: "capability_required" | "provider_unsupported" | "credential_invalid" | "credential_changed" | "reauthorization_required" | "refresh_failed" | "not_configured") { super(code); this.name = "ConnectionTokenError"; }
}
type CredentialRow = {
  id: string; provider: ConnectionProvider; account_id: string; account_display: string;
  capabilities: string[]; status: string; last_error_code: string; expires_at: Date | null; updated_at: Date;
  credential_ciphertext: Buffer; credential_nonce: Buffer; key_version: number;
};
/** Private adapter value. No endpoint or public SDK serializes this object. */
export type ConnectionTokenLease = {
  connectionId: string; purpose: ConnectionTokenPurpose; fingerprint: string;
  account: { provider: "google" | "microsoft"; accountId: string; display: string; status: string; errorCode: string };
  accessToken: string; tokenType: string;
};
const fingerprint = (row: Pick<CredentialRow, "key_version" | "credential_nonce" | "credential_ciphertext">) => createHash("sha256")
  .update(String(row.key_version)).update(row.credential_nonce).update(row.credential_ciphertext).digest("hex");

export function createConnectionTokenBroker(options: { pool: Pool; cipher: ConnectionCipher; refresh: TokenRefresher; now?: () => Date }) {
  const now = options.now ?? (() => new Date());
  const markHealth = async (tx: PoolClient, actor: SpaceActor, id: string, code: string) => {
    await tx.query("UPDATE connected_accounts SET status='needs_attention',last_error_code=$3,updated_at=now() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL", [id, actor.userId, code]);
  };
  const whileCurrent = <T>(actor: SpaceActor, lease: ConnectionTokenLease, operation: () => Promise<T>, pool: Pick<Pool, "connect"> = options.pool) => withTransaction(pool, async (tx) => {
      await requireConnectionActor(tx, actor, policies[lease.purpose].scope);
      const row = (await tx.query<Pick<CredentialRow, "credential_ciphertext" | "credential_nonce" | "key_version" | "capabilities">>(`SELECT credential_ciphertext,credential_nonce,key_version,capabilities FROM connected_accounts
        WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL FOR SHARE`, [lease.connectionId, actor.userId])).rows[0];
      if (!row) throw new SpaceError("not_found");
      try {
        if (!row.capabilities.includes(policies[lease.purpose].capability)) throw new ConnectionTokenError("capability_required");
        if (fingerprint(row) !== lease.fingerprint) throw new ConnectionTokenError("credential_changed");
      } finally { row.credential_ciphertext.fill(0); row.credential_nonce.fill(0); }
      return operation();
    }, { mode: "service" });
  return {
    assertCurrent: (actor: SpaceActor, lease: ConnectionTokenLease) => whileCurrent(actor, lease, async () => {}),
    // Serialize each bounded provider write with account/App/Space/connection
    // revocation. Never hold these locks across an entire multi-message batch.
    whileCurrent,
    acquire: async (actor: SpaceActor, rawId: string, purpose: ConnectionTokenPurpose, signal: AbortSignal): Promise<ConnectionTokenLease> => {
      const id = IdentifierSchema.safeParse(rawId.trim());
      if (!id.success) throw new SpaceError("invalid_request");
      // Health changes must commit before surfacing an expected provider failure.
      // Throwing that failure inside the transaction would silently roll it back.
      const outcome = await withTransaction(options.pool, async (tx): Promise<ConnectionTokenLease | ConnectionTokenError> => {
        await requireConnectionActor(tx, actor, policies[purpose].scope);
        const row = (await tx.query<CredentialRow>(`SELECT id,provider,account_id,account_display,capabilities,status,last_error_code,expires_at,updated_at,
          credential_ciphertext,credential_nonce,key_version FROM connected_accounts
          WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL FOR UPDATE`, [id.data, actor.userId])).rows[0];
        if (!row) throw new SpaceError("not_found");
        try {
          if (row.provider !== "google" && row.provider !== "microsoft") throw new ConnectionTokenError("provider_unsupported");
          if (!row.capabilities.includes(policies[purpose].capability)) throw new ConnectionTokenError("capability_required");
          signal.throwIfAborted();
          if (row.status === "needs_attention" && row.last_error_code === "reauthorization_required") return new ConnectionTokenError("reauthorization_required");
          if (row.status === "needs_attention" && row.last_error_code === "refresh_failed" && row.updated_at.getTime() + 30000 > now().getTime()) return new ConnectionTokenError("refresh_failed");
          let token: ProviderToken;
          try {
            const plaintext = options.cipher.decrypt(row.provider, row.credential_ciphertext, row.credential_nonce, row.key_version);
            try { token = providerTokenSchema.parse(JSON.parse(plaintext.toString("utf8"))); }
            finally { plaintext.fill(0); }
          } catch {
            await markHealth(tx, actor, row.id, "credential_invalid");
            return new ConnectionTokenError("credential_invalid");
          }
          const refreshWindow = Math.min(5 * 60000, token.expires_in ? token.expires_in * 100 : 5 * 60000);
          if (row.status !== "active" || row.expires_at && row.expires_at.getTime() <= now().getTime() + refreshWindow) {
            if (!token.refresh_token) {
              await markHealth(tx, actor, row.id, "reauthorization_required"); return new ConnectionTokenError("reauthorization_required");
            }
            let refreshed: ProviderToken;
            try {
              refreshed = providerTokenSchema.parse(await options.refresh(row.provider, token.refresh_token, signal));
              // Gmail and Graph issue expiring access tokens. An unusable expiry
              // must not trigger one refresh per waiting request or become eternal.
              if (!refreshed.expires_in) throw new TokenExchangeError("invalid_response");
            }
            catch (error) {
              if (signal.aborted) throw signal.reason;
              const code = error instanceof TokenExchangeError && (error.code === "not_configured" || error.code === "reauthorization_required") ? error.code : "refresh_failed";
              if (code !== "not_configured") await markHealth(tx, actor, row.id, code);
              return new ConnectionTokenError(code);
            }
            if (!refreshed.refresh_token) refreshed.refresh_token = token.refresh_token;
            const encoded = Buffer.from(JSON.stringify(refreshed));
            let encrypted: ReturnType<ConnectionCipher["encrypt"]>;
            try { encrypted = options.cipher.encrypt(row.provider, encoded); }
            finally { encoded.fill(0); }
            const expiresAt = new Date(now().getTime() + refreshed.expires_in! * 1000);
            row.credential_ciphertext.fill(0); row.credential_nonce.fill(0);
            row.credential_ciphertext = encrypted.ciphertext; row.credential_nonce = encrypted.nonce; row.key_version = encrypted.keyVersion;
            await tx.query(`UPDATE connected_accounts SET credential_ciphertext=$3,credential_nonce=$4,key_version=$5,expires_at=$6,
              status='active',last_error_code='',last_refreshed_at=now(),updated_at=now() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`,
              [row.id, actor.userId, encrypted.ciphertext, encrypted.nonce, encrypted.keyVersion, expiresAt]);
            row.status = "active"; row.last_error_code = ""; token = refreshed;
          }
          return { connectionId: row.id, purpose, fingerprint: fingerprint(row),
            account: { provider: row.provider, accountId: row.account_id, display: row.account_display, status: row.status, errorCode: row.last_error_code },
            accessToken: token.access_token, tokenType: token.token_type || "Bearer" };
        } finally { row.credential_ciphertext.fill(0); row.credential_nonce.fill(0); }
      }, { mode: "service" });
      // If cancellation arrived after a rotated token was received, its encrypted
      // replacement still commits; the canceled caller does not receive a lease.
      signal.throwIfAborted();
      if (outcome instanceof ConnectionTokenError) throw outcome;
      return outcome;
    },
    reportAuthorizationFailure: (actor: SpaceActor, lease: ConnectionTokenLease) => withTransaction(options.pool, async (tx) => {
      await requireConnectionActor(tx, actor, policies[lease.purpose].scope);
      const row = (await tx.query<Pick<CredentialRow, "credential_ciphertext" | "credential_nonce" | "key_version" | "capabilities">>(`SELECT credential_ciphertext,credential_nonce,key_version,capabilities FROM connected_accounts
        WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL FOR UPDATE`, [lease.connectionId, actor.userId])).rows[0];
      if (!row) return false;
      try {
        if (!row.capabilities.includes(policies[lease.purpose].capability) || fingerprint(row) !== lease.fingerprint) return false;
        await markHealth(tx, actor, lease.connectionId, "mail_provider_authorization_failed"); return true;
      } finally { row.credential_ciphertext.fill(0); row.credential_nonce.fill(0); }
    }, { mode: "service" }),
  };
}
