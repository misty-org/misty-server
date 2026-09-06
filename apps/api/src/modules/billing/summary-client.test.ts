import { generateKeyPair } from "jose";
import { expect, it, vi } from "vitest";
import { BillingUnavailable, createBillingSummaryClient, loadBillingSummaryClient } from "./summary-client.js";

const keys = await generateKeyPair("EdDSA", { crv: "Ed25519" });
const config = { endpoint: "https://payments.example.invalid/internal/billing/summary", keyId: "api-key", privateKey: keys.privateKey };
const identity = { version: 1, userId: "account", licenseId: "license" } as const;
const summary = { ...identity, subscription: null, hasCompletedPurchase: false };

it("requires a fixed trusted endpoint and rejects partial or self-host private payment configuration", async () => {
  for (const endpoint of ["http://payments.example.invalid/internal/billing/summary", "https://user:secret@payments.example.invalid/internal/billing/summary", `${config.endpoint}?query=secret`, `${config.endpoint}#fragment`, "https://payments.example.invalid/other"]) {
    expect(() => createBillingSummaryClient({ ...config, endpoint })).toThrow("Invalid private billing summary endpoint");
  }
  expect(() => createBillingSummaryClient({ ...config, endpoint: "http://127.0.0.1:8083/internal/billing/summary", allowInsecureLoopback: true })).not.toThrow();
  expect(await loadBillingSummaryClient({}, { deployment: "hosted", environment: "production" })).toBeNull();
  await expect(loadBillingSummaryClient({ PAYMENTS_SUMMARY_URL: config.endpoint }, { deployment: "hosted", environment: "production" })).rejects.toThrow("Invalid private billing client configuration");
  await expect(loadBillingSummaryClient({ PAYMENTS_SUMMARY_URL: config.endpoint, API_SIGNING_KEY_FILE: "private", API_SIGNING_KEY_ID: "api-key" }, { deployment: "self_hosted", environment: "production" })).rejects.toThrow("Invalid private billing client configuration");
});
it("rejects foreign identities, unknown fields, invalid bytes and oversized or incomplete responses", async () => {
  const responses = [Response.json({ ...summary, userId: "another" }), Response.json({ ...summary, licenseId: "another" }),
    Response.json({ ...summary, stripeSecret: "private" }), Response.json({ ...summary, hasCompletedPurchase: "false" }),
    new Response(new Uint8Array([0xff])), new Response(" ".repeat(8193)), new Response(JSON.stringify(summary), { headers: { "Content-Length": "9000" } }),
    new Response(JSON.stringify(summary), { headers: { "Content-Length": "1" } }), new Response("private provider error", { status: 503 })];
  for (const response of responses) {
    const reader = createBillingSummaryClient({ ...config, fetch: async () => response });
    await expect(reader(identity, new AbortController().signal)).rejects.toBeInstanceOf(BillingUnavailable);
  }
});
it("uses one bounded POST with no redirect or retry and never forwards the account credential", async () => {
  const send = vi.fn<typeof fetch>(async () => Response.json(summary));
  const read = createBillingSummaryClient({ ...config, fetch: send });
  expect(await read(identity, new AbortController().signal)).toEqual(summary);
  expect(send).toHaveBeenCalledTimes(1);
  expect(send.mock.calls[0]![1]).toMatchObject({ method: "POST", redirect: "error", body: Buffer.from(JSON.stringify(identity)) });
  expect(new Headers(send.mock.calls[0]![1]!.headers).get("Authorization")).toMatch(/^Bearer ey/);
  const failed = vi.fn<typeof fetch>(async () => { throw new Error("upstream private diagnostics"); });
  await expect(createBillingSummaryClient({ ...config, fetch: failed })(identity, new AbortController().signal)).rejects.toBeInstanceOf(BillingUnavailable);
  expect(failed).toHaveBeenCalledTimes(1);
  await expect(read(identity, AbortSignal.abort())).rejects.toBeInstanceOf(BillingUnavailable); expect(send).toHaveBeenCalledTimes(1);
});
it("bounds concurrent requests through streamed response completion and cancels stalled readers", async () => {
  let entered!: () => void, count = 0, cancelled = 0;
  const ready = new Promise<void>((resolve) => { entered = resolve; });
  const send: typeof fetch = async () => {
    if (++count > 16) return Response.json(summary);
    const stream = new ReadableStream({ cancel() { cancelled++; } });
    if (count === 16) entered();
    return new Response(stream);
  };
  const read = createBillingSummaryClient({ ...config, fetch: send }), controller = new AbortController();
  const pending = Array.from({ length: 16 }, () => read(identity, controller.signal).then(() => false, (error: unknown) => error instanceof BillingUnavailable));
  try {
    await ready;
    await expect(read(identity, controller.signal)).rejects.toBeInstanceOf(BillingUnavailable);
    controller.abort();
    expect(await Promise.all(pending)).toEqual(Array(16).fill(true));
    expect(cancelled).toBe(16);
    expect(await read(identity, new AbortController().signal)).toEqual(summary);
  } finally { controller.abort(); await Promise.all(pending); }
});

it("allows command-only configuration to own shared keys without implicitly enabling summaries", async () => {
  expect(await loadBillingSummaryClient({ PAYMENTS_COMMANDS_URL: "https://payments.example.invalid/internal/billing", API_SIGNING_KEY_FILE: "command-owned", API_SIGNING_KEY_ID: "key" }, { deployment: "hosted", environment: "test" })).toBeNull();
  await expect(loadBillingSummaryClient({ PAYMENTS_COMMANDS_URL: "https://payments.example.invalid/internal/billing" }, { deployment: "self_hosted", environment: "test" })).rejects.toThrow("configuration");
});
