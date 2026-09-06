import { generateKeyPair } from "jose";
import { expect, it, vi } from "vitest";
import { createBillingCommandClient, loadBillingCommandClient, BillingCommandConflict } from "./command-client.js";
import { BillingUnavailable } from "./summary-client.js";
const keys = await generateKeyPair("EdDSA", { crv: "Ed25519" });
const config = { endpoint: "https://payments.example.invalid/internal/billing", keyId: "command-key", privateKey: keys.privateKey };
const identity = { version: 1 as const, userId: "account", licenseId: "license" };
const intent = { ...identity, email: "user@example.invalid", tier: "pro" as const, interval: "month" as const, trialEligible: true };
const signal = () => new AbortController().signal;
it("rejects untrusted/private endpoints and incomplete or self-host signing configuration", async () => {
  for (const endpoint of ["http://payments.example.invalid/internal/billing", `${config.endpoint}?x=1`, `${config.endpoint}/other`, "https://name:secret@payments.example.invalid/internal/billing"]) {
    expect(() => createBillingCommandClient({ ...config, endpoint })).toThrow("Invalid private billing command endpoint");
  }
  expect(await loadBillingCommandClient({}, { deployment: "hosted", environment: "test" })).toBeNull();
  await expect(loadBillingCommandClient({ PAYMENTS_COMMANDS_URL: config.endpoint }, { deployment: "hosted", environment: "test" })).rejects.toThrow("configuration");
  await expect(loadBillingCommandClient({ PAYMENTS_COMMANDS_URL: config.endpoint, API_SIGNING_KEY_FILE: "unused", API_SIGNING_KEY_ID: "key" }, { deployment: "self_hosted", environment: "test" })).rejects.toThrow("configuration");
});
it("sends once, sanitizes failures and rejects hostile URLs, oversized or malformed responses", async () => {
  for (const response of [Response.json({ ...identity, url: "http://checkout.example.invalid" }), Response.json({ ...identity, url: "https://u:p@checkout.example.invalid" }),
    new Response("x".repeat(8193)), Response.json({ ...identity, url: "https://checkout.example.invalid", extra: "private" }), Response.json({ ...identity, userId: "other", url: "https://checkout.example.invalid" }), new Response(new Uint8Array([255])),
    new Response("{}", { headers: { "Content-Length": "3" } }), Response.json({ code: "private_customer_diagnostics" }, { status: 409 })]) {
    const send = vi.fn<typeof fetch>(async () => response);
    await expect(createBillingCommandClient({ ...config, fetch: send }).checkout(intent, signal())).rejects.toBeInstanceOf(BillingUnavailable);
    expect(send).toHaveBeenCalledTimes(1);
  }
  const send = vi.fn<typeof fetch>(async () => { throw new Error("provider request may have committed"); });
  await expect(createBillingCommandClient({ ...config, fetch: send }).checkout(intent, signal())).rejects.toBeInstanceOf(BillingUnavailable);
  expect(send).toHaveBeenCalledTimes(1);
  const conflict = createBillingCommandClient({ ...config, fetch: async () => Response.json({ code: "portal_unavailable" }, { status: 409 }) });
  await expect(conflict.portal(identity, signal())).rejects.toBeInstanceOf(BillingCommandConflict);
});
it("shares bounded streaming admission across commands and cancels stalled reads", async () => {
  let entered = 0, canceled = 0; const controllers = [new AbortController(), new AbortController()];
  let ready!: () => void; const started = new Promise<void>((resolve) => { ready = resolve; });
  const client = createBillingCommandClient({ ...config, fetch: async () => {
    if (++entered === 2) ready(); return new Response(new ReadableStream({ cancel() { canceled++; } }));
  } });
  const pending = [client.checkout(intent, controllers[0]!.signal), client.portal(identity, controllers[1]!.signal)].map((result) => result.catch((error: unknown) => error));
  try {
    await started; await expect(client.portal(identity, signal())).rejects.toBeInstanceOf(BillingUnavailable);
    controllers.forEach((controller) => controller.abort());
    expect((await Promise.all(pending)).every((error) => error instanceof BillingUnavailable)).toBe(true); expect(canceled).toBe(2);
  } finally { controllers.forEach((controller) => controller.abort()); await Promise.all(pending); }
});
