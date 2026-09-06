import { randomUUID } from "node:crypto";
import type { Pool, PoolClient } from "pg";
import { z } from "zod";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";
import type { SpaceActor } from "../../spaces/access.js";
import { requireConnectionActor } from "../access.js";
import type { ConnectionProvider } from "../config.js";
import type { ConnectionCipher } from "../credentials.js";
import { providerTokenSchema, type ProviderToken } from "../oauth-token.js";
import { ConnectionAuthorizationError, requestedAccess } from "./catalog.js";
import type { ConnectionIdentity } from "./identity.js";

const bindingSchema = z.object({ userId: z.string(), sessionHash: z.string().length(64).optional(), appSession: z.object({
  token_hash: z.string().length(64), user_id: z.string(), app_id: z.string(), space_id: z.string(), scopes: z.array(z.string()), expires_at: z.coerce.date(),
}).optional() }).refine((value) => !!value.sessionHash !== !!value.appSession);
export type AuthorizationActor = SpaceActor & { sessionHash?: string };
type Snapshot = { id: string; account_id: string; nonce: string; capabilities: string[] };
export type AuthorizationState = { state_hash: string; user_id: string; provider: ConnectionProvider; actor: AuthorizationActor;
  credential_snapshot: Snapshot[]; capabilities: string[]; requested_scopes: string[]; verifier_ciphertext: Buffer; verifier_nonce: Buffer;
  redirect_uri: string; client_id_hash: string; return_to: string; expires_at: Date };

export function createConnectionAuthorizationRepository(pool: Pool, cipher: ConnectionCipher, deployment: "hosted" | "self_hosted") {
  const current = async (tx: PoolClient, input: AuthorizationActor) => {
    const actor = bindingSchema.parse(input);
    await requireConnectionActor(tx, { userId: actor.userId, ...(actor.appSession ? { appSession: actor.appSession } : {}) }, "connections.write");
    if (actor.sessionHash && !(await tx.query("SELECT token_hash FROM sessions WHERE token_hash=$1 AND user_id=$2 AND expires_at>now() FOR SHARE", [actor.sessionHash, actor.userId])).rowCount) {
      throw new ConnectionAuthorizationError("authorization_expired");
    }
    // Callback browsers need not carry the desktop's session; verify its owner here.
    if (deployment === "self_hosted" && !(await tx.query(`SELECT user_id FROM self_host_accounts WHERE user_id=$1 AND disabled_at IS NULL AND entitlement_expires_at>now() FOR SHARE`, [actor.userId])).rowCount) {
      throw new ConnectionAuthorizationError("authorization_expired");
    }
    return actor;
  };
  return {
    begin: (input: { actor: AuthorizationActor; provider: ConnectionProvider; capabilities: string[]; stateHash: string; verifier: string;
      redirectUri: string; clientIdHash: string; returnTo: string }) => withTransaction(pool, async (tx) => {
      const actor = await current(tx, input.actor);
      // Serialize only issuance for this user, bounding persistent state across replicas.
      await tx.query("SELECT pg_advisory_xact_lock(hashtextextended($1,0))", [`connection-authorization:${actor.userId}`]);
      if ((await tx.query("SELECT 1 FROM connection_authorization_requests WHERE user_id=$1 AND created_at>now()-interval '10 minutes' LIMIT 20", [actor.userId])).rowCount === 20) {
        throw new ConnectionAuthorizationError("authorization_limit");
      }
      const accounts = (await tx.query<Snapshot & { capabilities: string[]; status: string }>(`SELECT id,account_id,encode(credential_nonce,'base64') AS nonce,capabilities,status
        FROM connected_accounts WHERE user_id=$1 AND provider=$2 ORDER BY id LIMIT 1001`, [actor.userId, input.provider])).rows;
      if (accounts.length > 1000) throw new ConnectionAuthorizationError("authorization_limit");
      const previous = input.provider === "figma" ? accounts.filter((account) => account.status === "active").flatMap((account) => account.capabilities) : [];
      const access = requestedAccess(input.provider, [...requestedAccess(input.provider, input.capabilities).capabilities, ...previous]);
      const encrypted = cipher.encrypt(input.provider, Buffer.from(input.verifier));
      const expiresAt = (await tx.query<{ expires_at: Date }>("SELECT LEAST(now()+interval '10 minutes', $1::timestamptz) AS expires_at", [actor.appSession?.expires_at ?? null])).rows[0]!.expires_at;
      try {
        await tx.query(`INSERT INTO connection_authorization_requests(state_hash,user_id,provider,actor,credential_snapshot,capabilities,requested_scopes,
          verifier_ciphertext,verifier_nonce,redirect_uri,client_id_hash,return_to,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
        [input.stateHash, actor.userId, input.provider, JSON.stringify(actor), JSON.stringify(accounts.map(({ id, account_id, nonce, capabilities, status }) => ({ id, account_id, nonce, capabilities: status === "revoked" ? [] : capabilities }))),
          JSON.stringify(access.capabilities), JSON.stringify(access.scopes), encrypted.ciphertext, encrypted.nonce, input.redirectUri, input.clientIdHash, input.returnTo, expiresAt]);
      } finally { encrypted.ciphertext.fill(0); encrypted.nonce.fill(0); }
      return { ...access, expiresAt };
    }, { mode: "service" }),
    consume: (provider: ConnectionProvider, stateHash: string, redirectUri: string, clientIdHash: string) => withTransaction(pool, async (tx) => {
      // Commit consumption before any provider exchange, including denied callbacks.
      const state = (await tx.query<AuthorizationState>(`SELECT * FROM connection_authorization_requests
        WHERE state_hash=$1 AND provider=$2 AND redirect_uri=$3 AND client_id_hash=$4 AND consumed_at IS NULL AND expires_at>now() FOR UPDATE`,
      [stateHash, provider, redirectUri, clientIdHash])).rows[0];
      if (!state) throw new ConnectionAuthorizationError("authorization_expired");
      await tx.query("UPDATE connection_authorization_requests SET consumed_at=now(),verifier_ciphertext=''::bytea,verifier_nonce=''::bytea WHERE state_hash=$1", [stateHash]);
      return state;
    }, { mode: "service" }),
    assertCurrent: (state: AuthorizationState) => withTransaction(pool, async (tx) => {
      if (state.actor.userId !== state.user_id) throw new ConnectionAuthorizationError("authorization_expired");
      await current(tx, state.actor);
      if (!(await tx.query("SELECT 1 FROM connection_authorization_requests WHERE state_hash=$1 AND consumed_at IS NOT NULL AND expires_at>now()", [state.state_hash])).rowCount) throw new ConnectionAuthorizationError("authorization_expired");
    }, { mode: "service" }),
    save: (state: AuthorizationState, identity: ConnectionIdentity, incoming: ProviderToken, access: { capabilities: string[]; scopes: string[] }) => withTransaction(pool, async (tx) => {
      if (state.actor.userId !== state.user_id) throw new ConnectionAuthorizationError("authorization_expired");
      await current(tx, state.actor);
      if (!(await tx.query("SELECT 1 FROM connection_authorization_requests WHERE state_hash=$1 AND consumed_at IS NOT NULL AND expires_at>now() FOR SHARE", [state.state_hash])).rowCount) throw new ConnectionAuthorizationError("authorization_expired");
      const old = (await tx.query<{ id: string; credential_nonce: Buffer; credential_ciphertext: Buffer; key_version: number; revoked_at: Date | null }>(`SELECT id,credential_nonce,credential_ciphertext,key_version,revoked_at
        FROM connected_accounts WHERE user_id=$1 AND provider=$2 AND account_id=$3 FOR UPDATE`, [state.user_id, state.provider, identity.id])).rows[0];
      const expected = state.credential_snapshot.find((account) => account.account_id === identity.id);
      try {
        // A refresh, removal, competing reconnect or delete/recreate invalidates the
        // old consent attempt. This also fences Go writers without modifying them.
        if (old ? !expected || expected.id !== old.id || expected.nonce !== old.credential_nonce.toString("base64") : !!expected) throw new ConnectionAuthorizationError("authorization_changed");
        const token = providerTokenSchema.parse(incoming);
        if (!token.refresh_token && old && !old.revoked_at) {
          let raw: Buffer | undefined;
          try { raw = cipher.decrypt(state.provider, old.credential_ciphertext, old.credential_nonce, old.key_version); token.refresh_token = providerTokenSchema.parse(JSON.parse(raw.toString("utf8"))).refresh_token; }
          catch { /* New consent can repair corrupt old credentials without carrying them forward. */ }
          finally { raw?.fill(0); }
        }
        const raw = Buffer.from(JSON.stringify(token));
        const encrypted = (() => { try { return cipher.encrypt(state.provider, raw); } finally { raw.fill(0); } })();
        try {
          const values = [old?.id ?? `connection_${randomUUID()}`, state.user_id, state.provider, identity.id, identity.display,
            encrypted.ciphertext, encrypted.nonce, encrypted.keyVersion, JSON.stringify(access.capabilities), JSON.stringify(access.scopes), token.expires_in ?? null];
          const result = old ? await tx.query(`UPDATE connected_accounts SET account_display=$5,credential_ciphertext=$6,credential_nonce=$7,key_version=$8,
            capabilities=$9,granted_scopes=$10,status='active',last_error_code='',revoked_at=NULL,expires_at=CASE WHEN $11::integer IS NULL THEN NULL ELSE now()+$11*interval '1 second' END,last_refreshed_at=now(),updated_at=now()
            WHERE id=$1 AND user_id=$2 AND provider=$3 AND account_id=$4 RETURNING id`, values) :
            await tx.query(`INSERT INTO connected_accounts(id,user_id,provider,account_id,account_display,credential_ciphertext,credential_nonce,key_version,capabilities,granted_scopes,expires_at)
              VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,CASE WHEN $11::integer IS NULL THEN NULL ELSE now()+$11*interval '1 second' END)
              ON CONFLICT(user_id,provider,account_id) DO NOTHING RETURNING id`, values);
          if (!result.rowCount) throw new ConnectionAuthorizationError("authorization_changed");
          return { id: result.rows[0]!.id as string, display: identity.display };
        } finally { encrypted.ciphertext.fill(0); encrypted.nonce.fill(0); }
      } finally { old?.credential_ciphertext.fill(0); old?.credential_nonce.fill(0); }
    }, { mode: "service" }),
  };
}
