import { generateKeyPair } from "jose";
import { beforeAll, describe, expect, it } from "vitest";
import { signServiceAssertion, verifyServiceAssertion } from "./service-assertion.js";

let keys: Awaited<ReturnType<typeof generateKeyPair>>;
beforeAll(async () => { keys = await generateKeyPair("EdDSA"); });
const now = new Date("2026-09-04T20:00:00Z");
const request = { method: "POST", path: "/internal/checkout", body: Buffer.from('{"tier":"pro"}') };
async function fixture() {
  const token = await signServiceAssertion({ privateKey: keys.privateKey, keyId: "api-1", issuer: "misty-api", audience: "misty-payments", subject: "account-1", scope: "billing:checkout", request, now });
  return { token, publicKeys: new Map([["api-1", keys.publicKey]]), issuer: "misty-api", audience: "misty-payments", scope: "billing:checkout", request, now };
}
describe("service assertion boundaries", () => {
  it("authenticates a request without exposing a shared signing secret", async () => {
    expect(await verifyServiceAssertion(await fixture())).toMatchObject({ subject: "account-1" });
  });
  it.each(["audience", "issuer", "scope"] as const)("rejects a different %s", async (key) => {
    await expect(verifyServiceAssertion({ ...await fixture(), [key]: "other" })).rejects.toThrow();
  });
  it("cannot be reused for a different URL or altered checkout body", async () => {
    const options = await fixture();
    await expect(verifyServiceAssertion({ ...options, request: { ...request, path: "/internal/portal" } })).rejects.toThrow();
    await expect(verifyServiceAssertion({ ...options, request: { ...request, body: Buffer.from('{"tier":"max"}') } })).rejects.toThrow();
  });
  it("rejects expired assertions", async () => {
    await expect(verifyServiceAssertion({ ...await fixture(), now: new Date(now.getTime() + 66000) })).rejects.toThrow();
  });
  it("rejects a revoked signing key", async () => {
    await expect(verifyServiceAssertion({ ...await fixture(), publicKeys: new Map() })).rejects.toThrow();
  });
});
