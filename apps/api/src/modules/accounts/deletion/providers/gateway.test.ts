import { expect, it, vi } from "vitest";
import { createProviderCleanupGateway, type CleanupOperation } from "./gateway.js";
import { ProviderCleanupError } from "./credentials.js";
const signal = () => new AbortController().signal;
it("uses fixed provider endpoints and correctly encodes credentials without redirects or retries", async () => {
  const send = vi.fn<typeof fetch>(async () => new Response(null, { status: 200 })), run = createProviderCleanupGateway(send);
  const token = "fixture-only+/=%";
  expect(await run({ kind: "google-token", token }, signal())).toBe("token_revoked");
  expect(await run({ kind: "dropbox-access-token", token }, signal())).toBe("token_revoked");
  expect(await run({ kind: "discord-token", token, tokenType: "refresh_token", clientId: "client :+", clientSecret: "secret +:" }, signal())).toBe("token_revoked");
  expect(send.mock.calls.map(call => call[0])).toEqual(["https://oauth2.googleapis.com/revoke", "https://api.dropboxapi.com/2/auth/token/revoke", "https://discord.com/api/v10/oauth2/token/revoke"]);
  for (const [, init] of send.mock.calls) expect(init).toMatchObject({ method: "POST", redirect: "error" });
  expect(new URLSearchParams(String(send.mock.calls[0]![1]!.body)).get("token")).toBe(token);
  expect(new Headers(send.mock.calls[1]![1]!.headers).get("Authorization")).toBe(`Bearer ${token}`);
  const third = send.mock.calls[2]![1]!;
  expect(Buffer.from(new Headers(third.headers).get("Authorization")!.slice(6), "base64").toString()).toBe("client+%3A%2B:secret+%2B%3A");
  expect(new URLSearchParams(String(third.body)).get("token_type_hint")).toBe("refresh_token");
});
it("recovers an already inactive Google token but does not mistake a Dropbox access failure for refresh revocation", async () => {
  expect(await createProviderCleanupGateway(async () => Response.json({ error: "invalid_token" }, { status: 400 }))({ kind: "google-token", token: "fixture" }, signal())).toBe("token_inactive");
  const run = createProviderCleanupGateway(async () => new Response(null, { status: 401 }));
  await expect(run({ kind: "dropbox-access-token", token: "fixture" }, signal())).rejects.toMatchObject({ code: "provider_access_token_rejected" });
  await expect(createProviderCleanupGateway(async () => Response.json({ error: "invalid_request" }, { status: 400 }))({ kind: "google-token", token: "fixture" }, signal())).rejects.toMatchObject({ code: "provider_cleanup_unavailable" });
});
it("requires an exact Figma deletion receipt and preserves ambiguous 404 outcomes", async () => {
  const id = "opaque/plus+%", operation: CleanupOperation = { kind: "figma-webhook", token: "fixture", webhookId: id };
  const send = vi.fn<typeof fetch>(async () => Response.json({ id }));
  expect(await createProviderCleanupGateway(send)(operation, signal())).toBe("webhook_deleted");
  expect(send.mock.calls[0]![0]).toBe("https://api.figma.com/v2/webhooks/opaque%2Fplus%2B%25");
  expect(send.mock.calls[0]![1]!.method).toBe("DELETE");
  for (const response of [Response.json({ id: "other" }), new Response(null, { status: 404 })]) {
    await expect(createProviderCleanupGateway(async () => response)(operation, signal())).rejects.toMatchObject({ code: "provider_cleanup_ambiguous" });
  }
  for (const webhookId of ["", ".", "..", "bad\n", "x".repeat(1025)]) await expect(createProviderCleanupGateway(send)({ ...operation, webhookId }, signal())).rejects.toMatchObject({ code: "provider_cleanup_invalid_request" });
  expect(send).toHaveBeenCalledTimes(1);
});
it("bounds provider bodies and sanitizes transport, malformed and transient failures", async () => {
  for (const response of [new Response("private", { status: 503 }), new Response("x".repeat(16385), { status: 400 }),
    new Response(new Uint8Array([255]), { status: 400 }), new Response("{}", { status: 400, headers: { "Content-Length": "9000000" } }),
    new Response("{}", { status: 400, headers: { "Content-Length": "3" } }), new Response("private", { status: 302 })]) {
    const send = vi.fn<typeof fetch>(async () => response);
    await expect(createProviderCleanupGateway(send)({ kind: "google-token", token: "fixture" }, signal())).rejects.toEqual(new ProviderCleanupError("provider_cleanup_unavailable"));
    expect(send).toHaveBeenCalledTimes(1);
  }
  const send = vi.fn<typeof fetch>(async () => { throw new Error("private provider diagnostics"); });
  await expect(createProviderCleanupGateway(send)({ kind: "google-token", token: "fixture" }, signal())).rejects.toEqual(new ProviderCleanupError("provider_cleanup_unavailable"));
  expect(send).toHaveBeenCalledTimes(1);
});
it("limits active effects, cancels stalled receipt reads and releases all admission slots", async () => {
  let entered = 0, canceled = 0, ready!: () => void; const started = new Promise<void>(resolve => { ready = resolve; });
  const controllers = Array.from({ length: 4 }, () => new AbortController());
  const run = createProviderCleanupGateway(async () => {
    if (++entered > 4) return new Response(null, { status: 200 });
    if (entered === 4) ready(); return new Response(new ReadableStream({ cancel() { canceled++; } }), { status: 400 });
  });
  const operation: CleanupOperation = { kind: "google-token", token: "fixture" };
  const pending = controllers.map(controller => run(operation, controller.signal).catch((error: unknown) => error));
  try {
    await started; await expect(run(operation, signal())).rejects.toBeInstanceOf(ProviderCleanupError);
    controllers.forEach(controller => controller.abort());
    expect((await Promise.all(pending)).every(error => error instanceof ProviderCleanupError)).toBe(true); expect(canceled).toBe(4);
    expect(await run(operation, signal())).toBe("token_revoked");
  } finally { controllers.forEach(controller => controller.abort()); await Promise.all(pending); }
});
