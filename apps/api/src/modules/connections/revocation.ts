import type { ConnectionCipher } from "./credentials.js";

export type RevocationCredential = { provider: string; credential_ciphertext: Buffer; credential_nonce: Buffer; key_version: number };
export type RevocationResult = "local_credentials_erased" | "provider_revoked" | "provider_revocation_failed_local_credentials_erased" | "provider_session_not_revocable_local_credentials_erased";
export type ConnectionRevoker = (credential: RevocationCredential, webhookIds: readonly string[], signal: AbortSignal) => Promise<RevocationResult>;

/** Fixed provider origins, no redirects/retries and one deadline for all cleanup. */
export function createConnectionRevoker(cipher: ConnectionCipher, fetcher: typeof fetch = fetch): ConnectionRevoker {
  return async (credential, webhookIds, signal) => {
    let accessToken: string;
    try {
      const plaintext = cipher.decrypt(credential.provider, credential.credential_ciphertext, credential.credential_nonce, credential.key_version);
      try {
        const token: unknown = JSON.parse(plaintext.toString("utf8"));
        if (!token || typeof token !== "object" || !("access_token" in token) || typeof token.access_token !== "string" || !token.access_token || /[\r\n\0]/.test(token.access_token)) return "local_credentials_erased";
        accessToken = token.access_token;
      } finally { plaintext.fill(0); }
    } catch { return "local_credentials_erased"; }

    const request = async (url: string, init: RequestInit): Promise<boolean> => {
      try {
        const response = await fetcher(url, { ...init, signal, redirect: "error" });
        // No response payload is needed. Do not buffer provider bodies or log tokens.
        await response.body?.cancel();
        return response.ok;
      } catch { return false; }
    };
    if (credential.provider === "google") {
      const revoked = await request("https://oauth2.googleapis.com/revoke", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams({ token: accessToken }) });
      return revoked ? "provider_revoked" : "provider_revocation_failed_local_credentials_erased";
    }
    if (credential.provider === "figma") {
      let next = 0;
      await Promise.all(Array.from({ length: Math.min(4, webhookIds.length) }, async () => {
        while (next < webhookIds.length && !signal.aborted) {
          const id = webhookIds[next++]!;
          if (!id || id === "." || id === ".." || id.length > 1024) continue;
          await request(`https://api.figma.com/v2/webhooks/${encodeURIComponent(id)}`, { method: "DELETE", headers: { Authorization: `Bearer ${accessToken}` } });
        }
      }));
    }
    return "provider_session_not_revocable_local_credentials_erased";
  };
}
