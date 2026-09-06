import { randomBytes } from "node:crypto";
import { z } from "zod";
import type { Pool } from "pg";
import { hashToken } from "../../auth/service.js";
import type { ConnectionOAuthClients } from "../config.js";
import type { ConnectionCipher } from "../credentials.js";
import { createOAuthTokenClient } from "../oauth-token.js";
import { authorizationUrl, connectionProvider, ConnectionAuthorizationError, grantedAccess } from "./catalog.js";
import type { ConnectionAuthorizationConfig } from "./config.js";
import { createConnectionIdentityReader } from "./identity.js";
import { createConnectionAuthorizationRepository, type AuthorizationActor } from "./repository.js";

export const connectionAuthorizationInput = z.object({ capabilities: z.array(z.string().min(1).max(80)).max(40).default([]),
  return_to: z.string().max(2048).default("").refine((value) => !value || value.startsWith("/") && !value.startsWith("//") && !/[\\\u0000-\u0020\u007f]/.test(value)),
}).strict();
const stateValue = z.string().regex(/^[A-Za-z0-9_-]{43}$/);
const codeValue = z.string().min(1).max(8192).regex(/^[\x21-\x7e]+$/);
export function createConnectionAuthorizationService(options: { pool: Pool; cipher: ConnectionCipher; clients: ConnectionOAuthClients;
  config: ConnectionAuthorizationConfig; deployment: "hosted" | "self_hosted"; fetcher?: typeof fetch }) {
  const repository = createConnectionAuthorizationRepository(options.pool, options.cipher, options.deployment);
  const tokens = createOAuthTokenClient(options.clients, options.fetcher), identity = createConnectionIdentityReader(options.fetcher);
  const configured = (name: string) => {
    const provider = connectionProvider(name), client = Object.hasOwn(options.clients, provider) ? options.clients[provider] : undefined;
    if (!client) throw new ConnectionAuthorizationError("provider_not_configured");
    return { provider, client, redirect: `${options.config.apiBase}/oauth/connections/${provider}/callback` };
  };
  return {
    async begin(actor: AuthorizationActor, name: string, body: unknown) {
      const { provider, client, redirect } = configured(name), parsed = connectionAuthorizationInput.safeParse(body);
      if (!parsed.success) throw new ConnectionAuthorizationError("invalid_request");
      const state = randomBytes(32).toString("base64url"), verifier = randomBytes(48).toString("base64url");
      const access = await repository.begin({ actor, provider, capabilities: parsed.data.capabilities, stateHash: hashToken(state), verifier,
        redirectUri: redirect, clientIdHash: hashToken(client.clientId), returnTo: parsed.data.return_to });
      return { provider, capabilities: access.capabilities, authorization_url: authorizationUrl(provider, client.clientId, redirect, state, verifier, access.scopes), state_expires_at: access.expiresAt.toISOString() };
    },
    async callback(name: string, query: URLSearchParams, requestSignal: AbortSignal) {
      const { provider, client, redirect } = configured(name);
      if (["state", "code", "error"].some((key) => query.getAll(key).length > 1) || !stateValue.safeParse(query.get("state")).success ||
        (!query.has("error") && !codeValue.safeParse(query.get("code")).success) || query.has("error") && query.has("code")) throw new ConnectionAuthorizationError("invalid_request");
      const state = await repository.consume(provider, hashToken(query.get("state")!), redirect, hashToken(client.clientId));
      let verifier: Buffer | undefined;
      try {
        if (query.has("error")) throw new ConnectionAuthorizationError("authorization_denied");
        await repository.assertCurrent(state);
        verifier = options.cipher.decrypt(provider, state.verifier_ciphertext, state.verifier_nonce, 1);
        const signal = AbortSignal.any([requestSignal, AbortSignal.timeout(40000)]);
        const token = await tokens.exchangeCode(provider, query.get("code")!, verifier.toString("utf8"), state.redirect_uri, signal);
        const account = await identity(provider, token, signal);
        const previousCapabilities = state.credential_snapshot.find((item) => item.account_id === account.id)?.capabilities ?? [];
        const access = grantedAccess(provider, state.requested_scopes, [...state.capabilities, ...previousCapabilities], token.scope);
        signal.throwIfAborted();
        return await repository.save(state, account, token, access);
      } catch (error) {
        if (error instanceof ConnectionAuthorizationError) throw error;
        // Provider, cipher, SQL and permission details never appear in callback HTML.
        throw new ConnectionAuthorizationError("authorization_unavailable");
      } finally { verifier?.fill(0); state.verifier_ciphertext.fill(0); state.verifier_nonce.fill(0); }
    },
  };
}
export type ConnectionAuthorizationService = ReturnType<typeof createConnectionAuthorizationService>;
