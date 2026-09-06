import { importJWK, jwtVerify, type CryptoKey } from "jose";
import { z } from "zod";
import { SelfHostProofRequired } from "../auth/model.js";

const claimsSchema = z.object({ sub: z.string().min(16).max(160), status: z.literal("eligible"), schema_version: z.literal(1),
  iat: z.number().int().positive(), exp: z.number().int().positive(), jti: z.string().min(1).max(256) });
export async function loadSelfHostPublicKeys(raw?: string) {
  const configured = raw ? z.record(z.string().min(1).max(100), z.string()).parse(JSON.parse(raw)) : {};
  const definitions = { "misty-2026-01": "d67rDV3YPzm1bhUKc5tmoML7qGAaVBEYi6GgfMoJQCA=", ...configured };
  if (Object.keys(definitions).length > 10) throw new Error("Too many self-host verification keys");
  return new Map(await Promise.all(Object.entries(definitions).map(async ([id, encoded]) => {
    const bytes = Buffer.from(encoded, "base64");
    if (bytes.length !== 32) throw new Error("Invalid self-host public key");
    return [id, await importJWK({ kty: "OKP", crv: "Ed25519", x: bytes.toString("base64url") }, "EdDSA") as CryptoKey] as const;
  })));
}
export function createSelfHostProofVerifier(keys: ReadonlyMap<string, CryptoKey>, clock = () => new Date()) {
  return async (token: string | undefined) => {
    try {
      if (!token || token.length > 8192) throw new Error("Missing proof");
      const now = clock();
      const { payload } = await jwtVerify(token.trim(), (header) => {
        const key = header.kid ? keys.get(header.kid) : undefined;
        if (!key) throw new Error("Unknown proof key");
        return key;
      }, { algorithms: ["EdDSA"], issuer: "misty-hosted", audience: "misty-self-hosted", currentDate: now, requiredClaims: ["sub", "iat", "exp", "jti"] });
      const claims = claimsSchema.parse(payload);
      if (claims.exp <= claims.iat || claims.exp - claims.iat > 7 * 86400 || claims.iat > Math.floor(now.getTime() / 1000) + 60) throw new Error("Invalid proof lifetime");
      return { subject: claims.sub, expiresAt: new Date(claims.exp * 1000) };
    } catch { throw new SelfHostProofRequired("Self-host entitlement is required"); }
  };
}
