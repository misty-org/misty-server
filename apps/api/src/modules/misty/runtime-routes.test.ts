import { expect, it, vi } from "vitest";
import { signRequest } from "../../../../agent-runtime/src/signature.js";
import { createRuntimeCallbackRoutes } from "./runtime-routes.js";

const secret = Buffer.alloc(32, 31), previous = Buffer.alloc(32, 32), now = Date.now();
const path = "/internal/agent-runtime/runs/invocation_test/activate";
const payload = JSON.stringify({ runtime_run_id: "workflow_1", runtime_kind: "vercel-workflow" });
function headers(body = payload, key = secret, timestamp = String(Math.floor(now / 1000)), signedPath = path) {
  return { "content-type": "application/json", "idempotency-key": "activation",
    "x-misty-agent-timestamp": timestamp, "x-misty-agent-signature": signRequest(key, "POST", signedPath, timestamp, Buffer.from(body)) };
}
function fixture() {
  const repository = { activate: vi.fn(async () => ({ run_id: "invocation_test", state: "running" })), event: vi.fn(async () => ({ accepted: true })) };
  return { repository, app: createRuntimeCallbackRoutes({ secrets: [secret, previous], repository, now: () => now, bodyDeadlineMs: 20 }) };
}
it("accepts actual workflow signatures and rotation, rejecting tampered body/path, stale or malformed signatures", async () => {
  const { app, repository } = fixture();
  for (const key of [secret, previous]) expect((await app.request(path, { method: "POST", body: payload, headers: headers(payload, key) })).status).toBe(200);
  for (const input of [
    { body: `${payload} `, headers: headers() },
    { body: payload, headers: headers(payload, secret, String(Math.floor(now / 1000) - 301)) },
    { body: payload, headers: headers(payload, secret, undefined, `${path}/other`) },
    { body: payload, headers: { ...headers(), "x-misty-agent-signature": `${headers()["x-misty-agent-signature"]}ff` } },
  ]) expect((await app.request(path, { method: "POST", ...input })).status).toBe(401);
  expect(repository.activate).toHaveBeenCalledTimes(2);
  expect((await app.request(path, { method: "POST", body: payload, headers: { ...headers(), "idempotency-key": "" } })).status).toBe(400);
});
it("bounds actual streamed bytes and stalled uploads before repository access, and fails closed when disabled", async () => {
  const { app, repository } = fixture();
  const huge = "x".repeat(2 * 1024 * 1024 + 1);
  expect((await app.request(path, { method: "POST", body: huge, headers: { ...headers(huge), "content-length": "1" } })).status).toBe(401);
  let canceled = false;
  const request = new Request(`http://localhost${path}`, { method: "POST", headers: headers(),
    body: new ReadableStream({ cancel() { canceled = true; } }), duplex: "half" } as RequestInit);
  expect((await app.request(request)).status).toBe(401);
  expect(canceled).toBe(true); expect(repository.activate).not.toHaveBeenCalled();
  const disabled = createRuntimeCallbackRoutes({ secrets: [], repository });
  expect((await disabled.request(path, { method: "POST", body: payload, headers: headers() })).status).toBe(503);
});
