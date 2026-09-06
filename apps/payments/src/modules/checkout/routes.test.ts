import { Hono } from "hono";
import { generateKeyPair } from "jose";
import { expect, it, vi } from "vitest";
import { signServiceAssertion } from "../../../../../packages/runtime/src/service-assertion.js";
import { createBillingCommandRoutes } from "./routes.js";

it("accepts only API-signed checkout commands bound to the authenticated account and action", async () => {
  const keys = await generateKeyPair("EdDSA", { crv: "Ed25519" });
  const create = vi.fn(async () => "https://checkout.stripe.com/test");
  const portal = vi.fn(async () => "https://billing.stripe.com/test");
  const api = new Hono().route("/internal/billing", createBillingCommandRoutes({
    publicKeys: new Map([["api-key", keys.publicKey]]), checkout: { create }, portal: { create: portal },
  }));
  const path = "/internal/billing/checkout";
  const command = { version: 1, userId: "user", licenseId: "license", email: "user@example.invalid", tier: "pro", interval: "month", trialEligible: true };
  const body = JSON.stringify(command);
  const sign = (subject: string, scope = "billing:checkout", value = body) => signServiceAssertion({
    privateKey: keys.privateKey, keyId: "api-key", issuer: "misty-api", audience: "misty-payments", subject, scope,
    request: { method: "POST", path, body: Buffer.from(value) },
  });
  const send = (token?: string, value = body) => api.request(path, { method: "POST", body: value,
    headers: { "Content-Type": "application/json", ...(token ? { Authorization: `Bearer ${token}` } : { Cookie: "misty_session=account-cookie" }) },
  });
  expect((await send()).status).toBe(401);
  expect((await send("downloaded-app-token")).status).toBe(401);
  expect((await send(await sign("another-user"))).status).toBe(403);
  expect((await send(await sign("user", "billing:portal"))).status).toBe(401);
  expect((await send(await sign("user"), body + " ")).status).toBe(401);
  const injected = JSON.stringify({ ...command, priceId: "price_attacker", successUrl: "https://attacker.invalid" });
  expect((await send(await sign("user", "billing:checkout", injected), injected)).status).toBe(400);
  expect(create).not.toHaveBeenCalled();
  const response = await send(await sign("user"));
  expect(response.status).toBe(200);
  expect(await response.json()).toEqual({ version: 1, userId: "user", licenseId: "license", url: "https://checkout.stripe.com/test" });
  expect(create).toHaveBeenCalledExactlyOnceWith(command);
  expect(portal).not.toHaveBeenCalled();
});
