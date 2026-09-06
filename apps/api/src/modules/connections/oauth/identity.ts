import { z } from "zod";
import type { ConnectionProvider } from "../config.js";
import { providerTokenSchema, type ProviderToken } from "../oauth-token.js";
import { ConnectionAuthorizationError, connectionOAuthCatalog } from "./catalog.js";

const string = z.string().regex(/^[^\u0000-\u001f\u007f]*$/).trim().min(1).max(320);
const object = z.record(z.string(), z.unknown());
export type ConnectionIdentity = { id: string; display: string };
export function createConnectionIdentityReader(fetcher: typeof fetch = fetch) {
  return async (provider: ConnectionProvider, credential: ProviderToken, requestSignal: AbortSignal): Promise<ConnectionIdentity> => {
    try {
      const token = providerTokenSchema.parse(credential), definition = connectionOAuthCatalog[provider], url = new URL(definition.identity);
      if (token.token_type && token.token_type.toLowerCase() !== "bearer") throw new Error("Unsupported token type");
      const headers = new Headers({ Accept: "application/json" });
      if (definition.identityQueryToken) url.searchParams.set("access_token", token.access_token);
      else headers.set("Authorization", `Bearer ${token.access_token}`);
      if (definition.identityPost) headers.set("Content-Type", "application/json");
      const response = await fetcher(url, { headers, method: definition.identityPost ? "POST" : "GET", redirect: "error",
        signal: AbortSignal.any([requestSignal, AbortSignal.timeout(15000)]) });
      const reader = response.body?.getReader();
      if (!reader) throw new Error("Empty identity");
      const chunks: Uint8Array[] = []; let bytes = 0;
      try {
        if (!response.ok) throw new Error("Identity refused");
        for (;;) { const next = await reader.read(); if (next.done) break; bytes += next.value.byteLength; if (bytes > 1024 * 1024) throw new Error("Identity too large"); chunks.push(next.value); }
        const raw = Buffer.concat(chunks, bytes);
        try {
          const value = object.parse(JSON.parse(raw.toString("utf8")));
          // Never coerce large numeric provider IDs through a lossy JavaScript number.
          const id = string.parse(value[provider === "google" ? "sub" : provider === "dropbox" ? "account_id" : "id"]);
          const display = [value.email, value.mail, value.userPrincipalName, value.username, value.displayName, value.handle, value.name, value.display_name]
            .map((candidate) => string.safeParse(candidate)).find((candidate) => candidate.success)?.data ?? `${definition.name} account`;
          return { id, display };
        } finally { raw.fill(0); }
      } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); for (const chunk of chunks) chunk.fill(0); }
    } catch { throw new ConnectionAuthorizationError("authorization_unavailable"); }
  };
}
