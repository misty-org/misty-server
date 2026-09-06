import { createHash, randomUUID } from "node:crypto";
import { SignJWT, jwtVerify } from "jose";
import type { CryptoKey } from "jose";
import { z } from "zod";

const claimsSchema = z.object({
  sub: z.string().min(1).max(256),
  scope: z.string().min(1).max(100),
  method: z.string().min(1).max(10),
  path: z.string().startsWith("/"),
  body_sha256: z.string().regex(/^[a-f0-9]{64}$/),
  jti: z.string().uuid(),
  iat: z.number().int(),
  exp: z.number().int(),
});
export type ServiceRequest = {
  method: string;
  path: string;
  body: Uint8Array;
};

function bodyHash(body: Uint8Array) {
  return createHash("sha256").update(body).digest("hex");
}

/** Private service authentication; never part of the downloaded-app SDK. */
export async function signServiceAssertion(options: {
  privateKey: CryptoKey;
  keyId: string;
  issuer: string;
  audience: string;
  subject: string;
  scope: string;
  request: ServiceRequest;
  now?: Date;
}): Promise<string> {
  const issuedAt = Math.floor((options.now ?? new Date()).getTime() / 1000);
  return new SignJWT({
    scope: options.scope,
    method: options.request.method.toUpperCase(),
    path: options.request.path,
    body_sha256: bodyHash(options.request.body),
  })
    .setProtectedHeader({ alg: "EdDSA", typ: "misty-service+jwt", kid: options.keyId })
    .setIssuer(options.issuer).setAudience(options.audience).setSubject(options.subject)
    .setJti(randomUUID()).setIssuedAt(issuedAt).setExpirationTime(issuedAt + 60)
    .sign(options.privateKey);
}

export async function verifyServiceAssertion(options: {
  token: string;
  publicKeys: ReadonlyMap<string, CryptoKey>;
  issuer: string;
  audience: string;
  scope: string;
  request: ServiceRequest;
  now?: Date;
}) {
  if (options.token.length > 8192) throw new Error("Invalid service assertion");
  const { payload } = await jwtVerify(options.token, (header) => {
    const key = typeof header.kid === "string" ? options.publicKeys.get(header.kid) : undefined;
    if (!key) throw new Error("Unknown service signing key");
    return key;
  }, {
    algorithms: ["EdDSA"], typ: "misty-service+jwt",
    issuer: options.issuer, audience: options.audience,
    requiredClaims: ["sub", "jti", "iat", "exp"],
    maxTokenAge: 60, clockTolerance: 5,
    currentDate: options.now ?? new Date(),
  });
  const claims = claimsSchema.parse(payload);
  if (claims.exp <= claims.iat || claims.exp - claims.iat > 60 || claims.scope !== options.scope ||
      claims.method !== options.request.method.toUpperCase() || claims.path !== options.request.path ||
      claims.body_sha256 !== bodyHash(options.request.body)) {
    throw new Error("Service assertion does not authorize this request");
  }
  return { subject: claims.sub, requestId: claims.jti };
}
