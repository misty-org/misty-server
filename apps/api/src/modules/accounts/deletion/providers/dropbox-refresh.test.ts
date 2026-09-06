import { expect, it, vi } from "vitest";
import { createDropboxCleanupRefresh } from "./dropbox-refresh.js";
import { ProviderCleanupError } from "./credentials.js";
const input = { refreshToken: "fixture-refresh+/=%", clientId: "fixture-client", clientSecret: "fixture-secret" };
const response = () => Response.json({ access_token: "fresh-access", token_type: "bearer", expires_in: 14400 });
const signal = () => new AbortController().signal;
it("refreshes at a fixed endpoint with encoded client identity and retains the reusable refresh token", async () => {
  const send = vi.fn<typeof fetch>(async () => response());
  expect(await createDropboxCleanupRefresh(send)(input, signal())).toEqual({ access_token: "fresh-access", refresh_token: input.refreshToken });
  expect(send).toHaveBeenCalledTimes(1); const [url, init] = send.mock.calls[0]!;
  expect(url).toBe("https://api.dropboxapi.com/oauth2/token"); expect(init).toMatchObject({ method: "POST", redirect: "error" });
  expect(Object.fromEntries(new URLSearchParams(String(init!.body)))).toEqual({ grant_type: "refresh_token", refresh_token: input.refreshToken, client_id: input.clientId, client_secret: input.clientSecret });
});
it("distinguishes rejected refresh credentials from transport, client-configuration and malformed response failures", async () => {
  await expect(createDropboxCleanupRefresh(async () => Response.json({ error: "invalid_grant" }, { status: 400 }))(input, signal())).rejects.toMatchObject({ code: "provider_refresh_rejected" });
  for (const reply of [Response.json({ error: "invalid_client" }, { status: 400 }), responseWith({ access_token: "fresh", token_type: "bearer", expires_in: 0 }),
    responseWith({ access_token: "bad\r\n", token_type: "bearer", expires_in: 14400 }), new Response("x".repeat(16385)), new Response(new Uint8Array([255])),
    new Response("{}", { headers: { "Content-Length": "4" } }), new Response("private-provider-error", { status: 503 })]) {
    const send = vi.fn<typeof fetch>(async () => reply);
    await expect(createDropboxCleanupRefresh(send)(input, signal())).rejects.toEqual(new ProviderCleanupError("provider_cleanup_unavailable")); expect(send).toHaveBeenCalledTimes(1);
  }
  const send = vi.fn<typeof fetch>(async () => { throw new Error("refresh may have reached the provider"); });
  await expect(createDropboxCleanupRefresh(send)(input, signal())).rejects.toEqual(new ProviderCleanupError("provider_cleanup_unavailable")); expect(send).toHaveBeenCalledTimes(1);
});
it("rejects malformed input and an already canceled operation without contacting Dropbox", async () => {
  const send = vi.fn<typeof fetch>(async () => response()), refresh = createDropboxCleanupRefresh(send);
  for (const bad of [{ ...input, refreshToken: "" }, { ...input, clientSecret: "bad\n" }, { ...input, clientId: "x".repeat(8193) }]) await expect(refresh(bad, signal())).rejects.toMatchObject({ code: "provider_cleanup_invalid_request" });
  const controller = new AbortController(); controller.abort(); await expect(refresh(input, controller.signal)).rejects.toBeInstanceOf(ProviderCleanupError);
  expect(send).not.toHaveBeenCalled();
});
it("bounds concurrent refreshes and cancels stalled replies when their callers stop", async () => {
  let count = 0, canceled = 0, ready!: () => void; const started = new Promise<void>(resolve => { ready = resolve; });
  const controllers = Array.from({ length: 4 }, () => new AbortController());
  const refresh = createDropboxCleanupRefresh(async () => { if (++count === 4) ready(); return new Response(new ReadableStream({ cancel() { canceled++; } })); });
  const pending = controllers.map(controller => refresh(input, controller.signal).catch((error: unknown) => error));
  try { await started; await expect(refresh(input, signal())).rejects.toBeInstanceOf(ProviderCleanupError); controllers.forEach(controller => controller.abort());
    expect((await Promise.all(pending)).every(error => error instanceof ProviderCleanupError)).toBe(true); expect(canceled).toBe(4);
  } finally { controllers.forEach(controller => controller.abort()); await Promise.all(pending); }
});
function responseWith(body: unknown) { return Response.json(body); }
