import { ProviderCleanupError } from "./credentials.js";
import { readCleanupJson } from "./response.js";

export type CleanupOperation =
  | { kind: "google-token"; token: string }
  | { kind: "dropbox-access-token"; token: string }
  | { kind: "discord-token"; token: string; tokenType: "access_token" | "refresh_token"; clientId: string; clientSecret: string }
  | { kind: "figma-webhook"; token: string; webhookId: string };
export type CleanupReceipt = "token_revoked" | "token_inactive" | "webhook_deleted";
const validSecret = (value: string) => value.length > 0 && Buffer.byteLength(value) <= 1024 * 1024 && !/[\r\n\0]/.test(value);

/** Exactly one provider effect. Its durable caller owns ordering, refresh-token
 * checkpointing and local erasure; this gateway never treats an outage as done. */
export function createProviderCleanupGateway(fetcher: typeof fetch = fetch) {
  let active = 0;
  return async (operation: CleanupOperation, requestSignal: AbortSignal): Promise<CleanupReceipt> => {
    if (active >= 4) throw new ProviderCleanupError("provider_cleanup_unavailable");
    active++;
    const signal = AbortSignal.any([requestSignal, AbortSignal.timeout(10000)]);
    try {
      signal.throwIfAborted();
      if (!validSecret(operation.token)) throw new ProviderCleanupError("provider_cleanup_invalid_request");
      let url: string; const headers = new Headers({ Accept: "application/json" }); let body: URLSearchParams | undefined;
      if (operation.kind === "figma-webhook") {
        const id = operation.webhookId;
        if (!id || id === "." || id === ".." || id.length > 1024 || /[\r\n\0]/.test(id)) throw new ProviderCleanupError("provider_cleanup_invalid_request");
        url = `https://api.figma.com/v2/webhooks/${encodeURIComponent(id)}`; headers.set("Authorization", `Bearer ${operation.token}`);
      } else if (operation.kind === "dropbox-access-token") {
        url = "https://api.dropboxapi.com/2/auth/token/revoke"; headers.set("Authorization", `Bearer ${operation.token}`);
      } else {
        url = operation.kind === "google-token" ? "https://oauth2.googleapis.com/revoke" : "https://discord.com/api/v10/oauth2/token/revoke";
        body = new URLSearchParams({ token: operation.token }); headers.set("Content-Type", "application/x-www-form-urlencoded");
        if (operation.kind === "discord-token") {
          if (!validSecret(operation.clientId) || !validSecret(operation.clientSecret)) throw new ProviderCleanupError("provider_cleanup_invalid_request");
          body.set("token_type_hint", operation.tokenType);
          const encode = (value: string) => new URLSearchParams({ x: value }).toString().slice(2);
          headers.set("Authorization", `Basic ${Buffer.from(`${encode(operation.clientId)}:${encode(operation.clientSecret)}`).toString("base64")}`);
        }
      }
      const response = await fetcher(url, { method: operation.kind === "figma-webhook" ? "DELETE" : "POST", headers, ...(body ? { body } : {}), signal, redirect: "error" });
      if (operation.kind === "figma-webhook" && response.status === 200) {
        const result = await readCleanupJson(response, signal);
        if (!result || typeof result !== "object" || !("id" in result) || result.id !== operation.webhookId) throw new ProviderCleanupError("provider_cleanup_ambiguous");
        return "webhook_deleted";
      }
      if (operation.kind === "google-token" && response.status === 400) {
        const result = await readCleanupJson(response, signal);
        if (result && typeof result === "object" && "error" in result && result.error === "invalid_token") return "token_inactive";
        throw new ProviderCleanupError("provider_cleanup_unavailable");
      }
      await response.body?.cancel(); signal.throwIfAborted();
      if (operation.kind !== "figma-webhook" && response.status === 200) return "token_revoked";
      // Figma's 404 also means insufficient permission, not proof of deletion.
      if (operation.kind === "figma-webhook" && response.status === 404) throw new ProviderCleanupError("provider_cleanup_ambiguous");
      // An expired Dropbox access token says nothing about its refresh token.
      if (operation.kind === "dropbox-access-token" && response.status === 401) throw new ProviderCleanupError("provider_access_token_rejected");
      throw new ProviderCleanupError("provider_cleanup_unavailable");
    } catch (error) { if (error instanceof ProviderCleanupError) throw error; throw new ProviderCleanupError("provider_cleanup_unavailable"); }
    finally { active--; }
  };
}
