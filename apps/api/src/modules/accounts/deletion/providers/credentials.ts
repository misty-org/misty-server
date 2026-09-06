import { z } from "zod";
import type { ConnectionCipher } from "../../../connections/credentials.js";

export class ProviderCleanupError extends Error {
  constructor(readonly code: "provider_credential_unreadable" | "provider_cleanup_unavailable" | "provider_cleanup_ambiguous" | "provider_cleanup_invalid_request" | "provider_access_token_rejected" | "provider_refresh_rejected") { super(code); }
}
const secret = z.string().max(1024 * 1024).regex(/^[^\r\n\0]*$/);
const tokenSchema = z.object({ access_token: secret.optional(), refresh_token: secret.optional() });
const legacySchema = z.object({ Token: tokenSchema, ClientID: secret.optional(), ClientSecret: secret.optional(), Custom: z.boolean().optional() });
export type DeletionCredential = { format: "connected" | "legacy"; provider: string; ciphertext: Buffer; nonce: Buffer; keyVersion: number };
export type CleanupTokens = { accessToken: string; refreshToken: string; customClient: { clientId: string; clientSecret: string } | null };

/** Encrypted bytes stay with the durable owner until cleanup is acknowledged.
 * A key/configuration failure is not evidence that a credential was revoked. */
export async function withDeletionCredential<T>(cipher: ConnectionCipher, input: DeletionCredential, operation: (tokens: CleanupTokens) => Promise<T>): Promise<T> {
  let plaintext: Buffer | undefined, tokens: CleanupTokens;
  try {
    plaintext = input.format === "connected" ? cipher.decrypt(input.provider, input.ciphertext, input.nonce, input.keyVersion)
      : cipher.decryptLegacy(input.provider, input.ciphertext, input.nonce, input.keyVersion);
    const value: unknown = JSON.parse(new TextDecoder("utf8", { fatal: true }).decode(plaintext));
    let token: z.infer<typeof tokenSchema>, customClient: CleanupTokens["customClient"] = null;
    if (input.format === "legacy" && value && typeof value === "object" && "Token" in value) {
      if ("access_token" in value || "refresh_token" in value) throw new Error("Ambiguous token envelope");
      const envelope = legacySchema.parse(value); token = envelope.Token;
      if (envelope.Custom) {
        if (!envelope.ClientID || !envelope.ClientSecret) throw new Error("Missing custom client");
        customClient = { clientId: envelope.ClientID, clientSecret: envelope.ClientSecret };
      }
    } else token = tokenSchema.parse(value);
    if (!token.access_token && !token.refresh_token) throw new Error("Missing tokens");
    tokens = { accessToken: token.access_token ?? "", refreshToken: token.refresh_token ?? "", customClient };
  } catch { throw new ProviderCleanupError("provider_credential_unreadable"); }
  finally { plaintext?.fill(0); }
  return operation(tokens);
}
