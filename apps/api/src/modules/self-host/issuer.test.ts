import { createPublicKey, generateKeyPairSync } from "node:crypto";
import { readFile } from "node:fs/promises";
import { decodeJwt, importJWK } from "jose";
import { expect, it } from "vitest";
import { createSelfHostIssuer, loadSelfHostSigningConfig } from "./issuer.js";
import { createSelfHostProofVerifier, loadSelfHostPublicKeys } from "./proof.js";

const key = generateKeyPairSync("ed25519").privateKey;
const env = { MISTY_SELF_HOST_ENTITLEMENT_PRIVATE_KEY: key.export({ type: "pkcs8", format: "der" }).toString("base64"),
  MISTY_SELF_HOST_ENTITLEMENT_KEY_ID: "test-key", MISTY_SELF_HOST_ENTITLEMENT_SUBJECT_SECRET: Buffer.alloc(32, 42).toString("base64") };
const now = new Date("2026-09-05T12:00:00Z");

it("verifies both existing Go and native Node proof fixtures with identical account binding", async () => {
  const fixture = JSON.parse(await readFile(new URL("../../../../../docs/migration/fixtures/self-host-proofs.json", import.meta.url), "utf8"));
  const keys = await loadSelfHostPublicKeys(JSON.stringify({ [fixture.keyId]: fixture.publicKey }));
  const verify = createSelfHostProofVerifier(keys, () => new Date(fixture.now));
  for (const token of [fixture.nodeToken, fixture.goToken]) {
    expect(await verify(token)).toEqual({ subject: fixture.subject, expiresAt: new Date(fixture.expiresAt) });
    await expect(createSelfHostProofVerifier(keys, () => new Date(fixture.expiresAt))(token)).rejects.toThrow();
  }
});

it("preserves stable private account subjects across signing-key rotation with independently unique proofs", async () => {
  const config = loadSelfHostSigningConfig(env)!, sign = createSelfHostIssuer(config);
  const publicKey = await importJWK(createPublicKey(key).export({ format: "jwk" }), "EdDSA");
  if (publicKey instanceof Uint8Array) throw new Error("Expected asymmetric key");
  const first = await sign("test-user", new Date(now.getTime() + 30 * 86400_000), now);
  const verified = await createSelfHostProofVerifier(new Map([[config.keyId, publicKey]]), () => now)(first.token);
  expect(verified.expiresAt.toISOString()).toBe(first.expires_at);
  expect(verified.expiresAt.getTime() - now.getTime()).toBe(7 * 86400_000);
  expect(verified.subject).toMatch(/^license_[A-Za-z0-9_-]{43}$/);
  const rotated = await createSelfHostIssuer({ ...config, privateKey: generateKeyPairSync("ed25519").privateKey, keyId: "rotated" })("test-user", verified.expiresAt, now);
  expect(decodeJwt(rotated.token).sub).toBe(verified.subject);
  expect(decodeJwt(rotated.token).jti).not.toBe(decodeJwt(first.token).jti);
  expect(decodeJwt((await sign("another-user", verified.expiresAt, now)).token).sub).not.toBe(verified.subject);
});

it("caps expiry at whole seconds and refuses expired or invalid eligibility", async () => {
  const sign = createSelfHostIssuer(loadSelfHostSigningConfig(env)!);
  const proof = await sign("test-user", new Date(now.getTime() + 1500), now);
  expect(proof.expires_at).toBe("2026-09-05T12:00:01.000Z");
  for (const end of [now, new Date(now.getTime() - 1), new Date("invalid")]) await expect(sign("test-user", end, now)).rejects.toThrow();
});

it("rejects incomplete, malformed and wrong-algorithm signer configuration without leaking secrets", () => {
  expect(loadSelfHostSigningConfig({})).toBeNull();
  const wrong = generateKeyPairSync("ec", { namedCurve: "P-256" }).privateKey.export({ type: "pkcs8", format: "der" }).toString("base64");
  for (const partial of [{ MISTY_SELF_HOST_ENTITLEMENT_PRIVATE_KEY: "" }, { MISTY_SELF_HOST_ENTITLEMENT_KEY_ID: "" },
    { MISTY_SELF_HOST_ENTITLEMENT_PRIVATE_KEY: wrong }, { MISTY_SELF_HOST_ENTITLEMENT_PRIVATE_KEY: env.MISTY_SELF_HOST_ENTITLEMENT_PRIVATE_KEY + "!" },
    { MISTY_SELF_HOST_ENTITLEMENT_SUBJECT_SECRET: "sensitive-invalid-value" }, { MISTY_SELF_HOST_ENTITLEMENT_SUBJECT_SECRET: Buffer.alloc(31).toString("base64") }]) {
    expect(() => loadSelfHostSigningConfig({ ...env, ...partial })).toThrow("Invalid self-host entitlement signing configuration");
  }
});
