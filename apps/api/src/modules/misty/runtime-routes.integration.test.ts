import { randomBytes, randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { afterAll, afterEach, beforeAll, expect, it, vi } from "vitest";
import { signRequest } from "../../../../agent-runtime/src/signature.js";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { createMistyAdmissionRepository } from "./admission.js";
import { createRuntimeCallbackRepository, type RuntimeInvocation } from "./runtime-repository.js";
import { createRuntimeCallbackRoutes } from "./runtime-routes.js";

const admin = createTestDatabase(), users: string[] = [], secret = randomBytes(32);
let application: Pool;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_runtime_callback_test') THEN
    CREATE ROLE misty_runtime_callback_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_runtime_callback_test;
    GRANT SELECT,UPDATE ON users,spaces,space_members,sessions,agent_conversations TO misty_runtime_callback_test;
    GRANT SELECT,INSERT,UPDATE ON ai_user_settings,ai_invocations TO misty_runtime_callback_test;
    GRANT SELECT,INSERT ON ai_invocation_events,ai_runtime_callback_receipts TO misty_runtime_callback_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_runtime_callback_test", max: 5 });
}, 60000);
afterEach(async () => { await admin.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]); users.length = 0; });
afterAll(async () => { if (application) await application.end(); await admin.end(); });
async function fixture() {
  const userId = `runtime_${randomUUID().replaceAll("-", "").slice(0, 12)}`, sessionHash = randomUUID(); users.push(userId);
  await withTransaction(admin, async tx => {
    await tx.query("INSERT INTO users(id,email,password_hash,username,license_id) VALUES($1,$2,'test-only',$1,$3)", [userId, `${userId}@example.invalid`, `license_${userId}`]);
    await tx.query("INSERT INTO licenses(id,user_id) VALUES($1,$2)", [`license_${userId}`, userId]);
    await tx.query("INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '1 day')", [sessionHash, userId]);
  });
  const { invocation } = await createMistyAdmissionRepository(application).create({ userId, sessionHash }, {
    surfaceId: "home", mode: "quick", trigger: "message", idempotencyKey: randomUUID(), payload: { prompt: "hello" },
  });
  const reserve = vi.fn(async (_: RuntimeInvocation) => undefined);
  const app = createRuntimeCallbackRoutes({ secrets: [secret], repository: createRuntimeCallbackRepository(application, { reserveModelUsage: reserve }) });
  const runtimeId = randomUUID();
  async function request(action: "activate" | "events", data: object, headerKey = randomUUID()) {
    const path = `/internal/agent-runtime/runs/${invocation.id}/${action}`, timestamp = String(Math.floor(Date.now() / 1000));
    const body = Buffer.from(JSON.stringify({ runtime_run_id: runtimeId, ...data }));
    return app.request(path, { method: "POST", body, headers: { "content-type": "application/json", "idempotency-key": headerKey,
      "x-misty-agent-timestamp": timestamp, "x-misty-agent-signature": signRequest(secret, "POST", path, timestamp, body) } });
  }
  const activate = () => request("activate", { runtime_kind: "vercel-workflow" });
  const events = async () => (await admin.query("SELECT payload FROM ai_invocation_events WHERE invocation_id=$1 ORDER BY sequence", [invocation.id])).rows.map(row => row.payload);
  return { userId, invocation, runtimeId, request, activate, reserve, events };
}
it("activates and projects workflow node events once, preserving stream format and invocation state", async () => {
  const f = await fixture();
  expect((await f.activate()).status).toBe(200); expect((await f.activate()).status).toBe(200);
  const tool = { node_id: "tool:call_1", phase: "using_context_get", state: "running", attempt: 1, progress: 20, output: {} };
  const results = await Promise.all([f.request("events", tool), f.request("events", tool)]);
  expect(results.map(r => r.status)).toEqual([200, 200]);
  expect((await f.request("events", { ...tool, state: "completed" })).status).toBe(200);
  expect((await f.request("events", { node_id: "model:1", state: "completed", phase: "thinking", output: { text_delta: "Hello" } })).status).toBe(200);
  expect(await f.events()).toEqual([
    { id: "1", type: "invocation.started", state: "running" }, { id: "2", type: "assistant.status", phase: "thinking" },
    { id: "3", type: "assistant.status", phase: "tool", text: "Using context get…" },
    { id: "4", type: "tool.started", toolCallId: "call_1", toolName: "context.get" },
    { id: "5", type: "tool.completed", toolCallId: "call_1", toolName: "context.get" },
    { id: "6", type: "response.delta", delta: "Hello" },
  ]);
  expect((await admin.query("SELECT state FROM ai_invocations WHERE id=$1", [f.invocation.id])).rows[0].state).toBe("running");
  expect((await f.request("events", { ...tool, output: { changed: true } })).status).toBe(409);
  expect(await f.events()).toHaveLength(6);
});
it("rejects legacy ownership, wrong bindings and disabled or expired accounts without accepting events", async () => {
  const f = await fixture();
  await admin.query("UPDATE ai_invocations SET runtime_owner='go' WHERE id=$1", [f.invocation.id]);
  expect((await f.activate()).status).toBe(409);
  await admin.query("UPDATE ai_invocations SET runtime_owner='hono' WHERE id=$1", [f.invocation.id]);
  expect((await f.activate()).status).toBe(200);
  const event = { node_id: "model:1", state: "running", phase: "thinking", output: {} };
  expect((await f.request("events", { ...event, runtime_run_id: "other" })).status).toBe(409);
  await admin.query("UPDATE ai_invocations SET expires_at=now()-interval '1 second' WHERE id=$1", [f.invocation.id]);
  expect((await f.request("events", event)).status).toBe(409);
  await admin.query("UPDATE ai_invocations SET expires_at=NULL WHERE id=$1", [f.invocation.id]);
  await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [f.userId]);
  expect((await f.request("events", event)).status).toBe(409);
  expect(f.reserve).not.toHaveBeenCalled(); expect(await f.events()).toHaveLength(2);
});
it("requires usage admission for model starts and rechecks lifecycle after that independent operation", async () => {
  const f = await fixture(); await f.activate();
  const event = { node_id: "model:1", state: "running", phase: "thinking", output: {} };
  expect((await f.request("events", event)).status).toBe(200);
  expect((await f.request("events", event)).status).toBe(200);
  expect(f.reserve).toHaveBeenCalledTimes(1);
  expect(f.reserve.mock.calls[0]![0]).toMatchObject({ id: f.invocation.id, user_id: f.userId, request_payload: { prompt: "hello" } });
  f.reserve.mockImplementationOnce(async () => { await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [f.userId]); });
  expect((await f.request("events", { ...event, node_id: "model:2" })).status).toBe(409);
  expect((await admin.query("SELECT count(*) FROM ai_runtime_callback_receipts WHERE invocation_id=$1", [f.invocation.id])).rows[0].count).toBe("2");
});
