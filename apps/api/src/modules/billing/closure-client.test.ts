import { generateKeyPair } from "jose";
import { expect, it, vi } from "vitest";
import { verifyServiceAssertion } from "../../../../../packages/runtime/src/service-assertion.js";
import { createBillingClosureClient, BillingClosureUnavailable } from "./closure-client.js";
const keys = await generateKeyPair("EdDSA", { crv: "Ed25519" });
const config = { endpoint: "https://payments.example.invalid/internal/billing", keyId: "closure-key", privateKey: keys.privateKey };
const intent = { version: 1 as const, userId: "account", licenseId: "license", deletionRequestId: "deletion-original" };
const signal = () => new AbortController().signal;
it("binds the private closure assertion to the original account, deletion ID and exact request", async () => {
  const client = createBillingClosureClient({ ...config, fetch: async (url, init) => {
    const request = new Request(url, init), body = new Uint8Array(await request.arrayBuffer());
    expect(request.redirect).toBe("error"); expect(JSON.parse(Buffer.from(body).toString())).toEqual(intent);
    const verified = await verifyServiceAssertion({ token: request.headers.get("Authorization")!.slice(7), publicKeys: new Map([[config.keyId, keys.publicKey]]),
      issuer: "misty-api", audience: "misty-payments", scope: "billing:account-closure", request: { method: "POST", path: "/internal/billing/account-closure", body } });
    expect(verified.subject).toBe(intent.userId); return Response.json({ ...intent, state: "closing" });
  } });
  expect(await client.close(intent, signal())).toEqual({ ...intent, state: "closing" });
  for (const endpoint of ["http://payments.example.invalid/internal/billing", `${config.endpoint}/other`, `${config.endpoint}?x=1`, `${config.endpoint}#x`, "https://user:secret@payments.example.invalid/internal/billing"]) {
    expect(() => createBillingClosureClient({ ...config, endpoint })).toThrow("Invalid private billing closure endpoint");
  }
});
it("rejects mismatched identities and malformed or oversized receipts without retry or provider diagnostics", async () => {
  for (const response of [Response.json({ ...intent, userId: "other", state: "closed" }), Response.json({ ...intent, licenseId: "other", state: "closed" }),
    Response.json({ ...intent, deletionRequestId: "other", state: "closed" }), Response.json({ ...intent, state: "closed", extra: "private" }),
    Response.json({ ...intent, state: "accepted" }), new Response("x".repeat(4097)), new Response(new Uint8Array([255])),
    new Response("{}", { headers: { "Content-Length": "3" } }), new Response("{}", { headers: { "Content-Length": "4097" } }),
    new Response("private provider diagnostics", { status: 503 }), new Response("", { status: 302, headers: { Location: "https://other.invalid" } })]) {
    const send = vi.fn<typeof fetch>(async () => response);
    await expect(createBillingClosureClient({ ...config, fetch: send }).close(intent, signal())).rejects.toEqual(new BillingClosureUnavailable());
    expect(send).toHaveBeenCalledTimes(1);
  }
  const send = vi.fn<typeof fetch>(async () => { throw new Error("remote request may have committed"); });
  await expect(createBillingClosureClient({ ...config, fetch: send }).close(intent, signal())).rejects.toEqual(new BillingClosureUnavailable());
  expect(send).toHaveBeenCalledTimes(1);
});
it("bounds concurrent streaming receipts, cancels stalled bodies and releases admission", async () => {
  let entered = 0, canceled = 0, ready!: () => void; const started = new Promise<void>(resolve => { ready = resolve; });
  const controllers = [new AbortController(), new AbortController()];
  const client = createBillingClosureClient({ ...config, fetch: async () => {
    if (++entered > 2) return Response.json({ ...intent, state: "closed" });
    if (entered === 2) ready(); return new Response(new ReadableStream({ cancel() { canceled++; } }));
  } });
  const pending = controllers.map(controller => client.close(intent, controller.signal).catch((error: unknown) => error));
  try {
    await started; await expect(client.close(intent, signal())).rejects.toBeInstanceOf(BillingClosureUnavailable);
    controllers.forEach(controller => controller.abort());
    expect((await Promise.all(pending)).every(error => error instanceof BillingClosureUnavailable)).toBe(true); expect(canceled).toBe(2);
    expect((await client.close(intent, signal())).state).toBe("closed");
  } finally { controllers.forEach(controller => controller.abort()); await Promise.all(pending); }
});
