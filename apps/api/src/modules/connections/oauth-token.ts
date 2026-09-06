import { z } from "zod";
import type { ConnectionOAuthClients, ConnectionProvider } from "./config.js";

const opaque = z.string().max(512 * 1024).regex(/^[\x21-\x7e]*$/);
export const providerTokenSchema = z.object({
  access_token: opaque.min(1), refresh_token: opaque.optional(),
  token_type: z.string().regex(/^[A-Za-z][A-Za-z0-9_-]{0,63}$/).optional(),
  scope: z.string().max(128 * 1024).optional(), expires_in: z.number().int().min(0).max(315360000).optional(),
  id_token: opaque.optional(), team: z.object({ id: z.string(), name: z.string() }).optional(),
  workspace_id: z.string().optional(), workspace_name: z.string().optional(),
});
export type ProviderToken = z.infer<typeof providerTokenSchema>;
export class TokenExchangeError extends Error {
  constructor(readonly code: "not_configured" | "invalid_response" | "response_too_large" | "reauthorization_required" | "unavailable") { super(code); this.name = "TokenExchangeError"; }
}
const tokenEndpoints: Record<ConnectionProvider, string> = {
  google: "https://oauth2.googleapis.com/token",
  microsoft: "https://login.microsoftonline.com/common/oauth2/v2.0/token",
  dropbox: "https://api.dropboxapi.com/oauth2/token",
  figma: "https://api.figma.com/v1/oauth/token",
  discord: "https://discord.com/api/v10/oauth2/token",
  instagram: "https://api.instagram.com/oauth/access_token",
};
export type TokenRefresher = (provider: ConnectionProvider, refreshToken: string, signal: AbortSignal) => Promise<ProviderToken>;

/** Provider endpoints and client authentication never come from a request body. */
export function createOAuthTokenClient(clients: ConnectionOAuthClients, fetcher: typeof fetch = fetch) {
  const exchange = async (provider: ConnectionProvider, values: URLSearchParams, requestSignal: AbortSignal): Promise<ProviderToken> => {
    const client = clients[provider], endpoint = provider === "figma" && values.get("grant_type") === "refresh_token" ? "https://api.figma.com/v1/oauth/refresh" : tokenEndpoints[provider];
    if (!Object.hasOwn(clients, provider) || !Object.hasOwn(tokenEndpoints, provider) || !client || !endpoint) throw new TokenExchangeError("not_configured");
    const headers = new Headers({ "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" });
    if (provider === "figma") headers.set("Authorization", `Basic ${Buffer.from(`${client.clientId}:${client.clientSecret}`).toString("base64")}`);
    else { values.set("client_id", client.clientId); values.set("client_secret", client.clientSecret); }
    const signal = AbortSignal.any([requestSignal, AbortSignal.timeout(20000)]);
    try {
      const response = await fetcher(endpoint, { method: "POST", headers, body: values, signal, redirect: "error" });
      const reader = response.body?.getReader();
      if (!reader) throw new TokenExchangeError("invalid_response");
      const chunks: Uint8Array[] = []; let bytes = 0;
      try {
        for (;;) {
          const next = await reader.read(); if (next.done) break;
          bytes += next.value.byteLength;
          if (bytes > 1024 * 1024) throw new TokenExchangeError("response_too_large");
          chunks.push(next.value);
        }
      } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
      const raw = Buffer.concat(chunks, bytes);
      try {
        let result: unknown;
        try { result = JSON.parse(raw.toString("utf8")); } catch { throw new TokenExchangeError("invalid_response"); }
        if (!response.ok) {
          const rejected = response.status === 400 && !!result && typeof result === "object" && "error" in result && result.error === "invalid_grant";
          throw new TokenExchangeError(rejected ? "reauthorization_required" : "unavailable");
        }
        // Some Instagram code responses report granted permissions separately.
        // Preserve that evidence instead of treating omitted OAuth scope as full consent.
        if (provider === "instagram" && result && typeof result === "object" && "permissions" in result) {
          const permissions = z.array(z.string().min(1).max(256).regex(/^[A-Za-z0-9_]+$/)).max(100).safeParse(result.permissions);
          if (!permissions.success) throw new TokenExchangeError("invalid_response");
          result = { ...result, scope: permissions.data.join(" ") };
        }
        const token = providerTokenSchema.safeParse(result);
        if (!token.success) throw new TokenExchangeError("invalid_response");
        return token.data;
      } finally { raw.fill(0); for (const chunk of chunks) chunk.fill(0); }
    } catch (error) {
      // Neither provider body text nor fetch/Zod messages become application errors.
      if (error instanceof TokenExchangeError) throw error;
      throw new TokenExchangeError("unavailable");
    }
  };
  return {
    refresh: ((provider, refreshToken, signal) => {
      if (!opaque.min(1).safeParse(refreshToken).success) throw new TokenExchangeError("invalid_response");
      return exchange(provider, new URLSearchParams({ grant_type: "refresh_token", refresh_token: refreshToken }), signal);
    }) satisfies TokenRefresher,
    exchangeCode: (provider: ConnectionProvider, code: string, verifier: string, redirectUri: string, signal: AbortSignal) => {
      const values = new URLSearchParams({ grant_type: "authorization_code", code, redirect_uri: redirectUri });
      if (provider !== "instagram") values.set("code_verifier", verifier);
      return exchange(provider, values, signal);
    },
  };
}
