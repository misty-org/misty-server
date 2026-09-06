import { randomUUID } from "node:crypto";
import { Hono } from "hono";
import { generateKeyPair } from "jose";
import { expect, it, vi } from "vitest";
import { signServiceAssertion } from "../../../../../packages/runtime/src/service-assertion.js";
import { createEntitlementRoutes } from "./routes.js";

it("requires an assertion bound to the exact body, service, and affected account", async () => {
  const keys = await generateKeyPair("EdDSA", { crv: "Ed25519" });
  const receive = vi.fn(async () => "applied" as const);
  const path = "/internal/payments/entitlements";
  const api = new Hono().route(path, createEntitlementRoutes({ publicKeys: new Map([["test-key", keys.publicKey]]), repository: { receive } }));
  const event = { version: 1, eventId: randomUUID(), userId: "user", licenseId: "license", revision: "1", generatedAt: new Date().toISOString(), subscription: null };
  const body = JSON.stringify(event);
  const assertion = (subject: string, value = body) => signServiceAssertion({
    privateKey: keys.privateKey, keyId: "test-key", issuer: "misty-payments", audience: "misty-api", subject,
    scope: "entitlements:write", request: { method: "POST", path, body: Buffer.from(value) },
  });
  const send = (value: string, authorization?: string) => api.request(path, {
    method: "POST", body: value, headers: { "Content-Type": "application/json", ...(authorization ? { Authorization: `Bearer ${authorization}` } : { Cookie: "misty_session=account-session" }) },
  });
  expect((await send(body)).status).toBe(401);
  expect((await send(body, "downloaded-app-token")).status).toBe(401);
  expect((await send(body, await assertion("another-user"))).status).toBe(403);
  expect((await send(body + " ", await assertion("user"))).status).toBe(401);
  const invalid = JSON.stringify({ ...event, revision: 2 });
  expect((await send(invalid, await assertion("user", invalid))).status).toBe(400);
  expect(receive).not.toHaveBeenCalled();
  const response = await send(body, await assertion("user"));
  expect(response.status).toBe(200);
  expect(await response.json()).toEqual({ eventId: event.eventId, result: "applied" });
  expect(receive).toHaveBeenCalledExactlyOnceWith(event);
});
