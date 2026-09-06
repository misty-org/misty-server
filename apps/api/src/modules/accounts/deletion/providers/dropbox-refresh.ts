import { z } from "zod";
import { ProviderCleanupError } from "./credentials.js";
import { readCleanupJson } from "./response.js";
const opaque = z.string().min(1).max(8192).regex(/^[\x21-\x7e]+$/);
const reply = z.object({ access_token: opaque, refresh_token: opaque.optional(), token_type: z.literal("bearer").or(z.literal("Bearer")), expires_in: z.number().int().positive().max(315360000) });
export type DropboxRefreshInput = { refreshToken: string; clientId: string; clientSecret: string };
/** Dropbox refresh tokens are reusable. No automatic network retry is made here;
 * the durable account resource owns the next attempt and the revocation proof. */
export function createDropboxCleanupRefresh(fetcher: typeof fetch = fetch) {
  let active = 0;
  return async (input: DropboxRefreshInput, requestSignal: AbortSignal) => {
    if (active >= 4) throw new ProviderCleanupError("provider_cleanup_unavailable");
    active++; const signal = AbortSignal.any([requestSignal, AbortSignal.timeout(10000)]);
    try {
      signal.throwIfAborted();
      if (![input.refreshToken, input.clientId, input.clientSecret].every(value => opaque.safeParse(value).success)) throw new ProviderCleanupError("provider_cleanup_invalid_request");
      const response = await fetcher("https://api.dropboxapi.com/oauth2/token", { method: "POST", signal, redirect: "error",
        headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
        body: new URLSearchParams({ grant_type: "refresh_token", refresh_token: input.refreshToken, client_id: input.clientId, client_secret: input.clientSecret }) });
      const value = await readCleanupJson(response, signal);
      if (response.status === 400 && value && typeof value === "object" && "error" in value && value.error === "invalid_grant") throw new ProviderCleanupError("provider_refresh_rejected");
      if (response.status !== 200) throw new ProviderCleanupError("provider_cleanup_unavailable");
      const result = reply.parse(value);
      return { access_token: result.access_token, refresh_token: result.refresh_token ?? input.refreshToken };
    } catch (error) { if (error instanceof ProviderCleanupError) throw error; throw new ProviderCleanupError("provider_cleanup_unavailable"); }
    finally { active--; }
  };
}
