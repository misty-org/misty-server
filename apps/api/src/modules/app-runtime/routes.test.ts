import { describe, expect, it, vi } from "vitest";
import { createApi } from "../../app.js";
import { createLogger } from "../../../../../packages/runtime/src/logger.js";
import { loadRuntimeConfig } from "../../../../../packages/runtime/src/config.js";
import type { AppRuntimeRepository, AppSession } from "./repository.js";
import type { AppRuntimeDependencies } from "./routes.js";
import { Hono } from "hono";
import { createNativeDispatcher } from "./dispatch.js";

function fixture(overrides: Partial<AppSession> = {}) {
  const session: AppSession = { token_hash: "a".repeat(64), user_id: "account-1", app_id: "notes", space_id: "space-1", scopes: ["notes.read", "storage.read", "storage.write"], expires_at: new Date(Date.now() + 60000), ...overrides };
  const repository: AppRuntimeRepository = {
    findSession: vi.fn(async () => session),
    isSpaceMember: vi.fn(async () => true),
    listRecords: vi.fn(async () => []),
    putRecord: vi.fn(async (_session, key, data) => ({ key, data, created_at: new Date(), updated_at: new Date() })),
    deleteRecord: vi.fn(async () => true),
  };
  const dispatch = vi.fn<NonNullable<AppRuntimeDependencies["dispatch"]>>(async () => Response.json({ notes: [] }));
  const app = createApi({
    logger: createLogger(loadRuntimeConfig("api", { LOG_LEVEL: "silent" })),
    checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    appRuntime: { repository, dispatch },
  });
  const rpc = (body: unknown) => app.request("/v1/app-runtime/rpc", { method: "POST", headers: { Authorization: "Bearer app-token", "Content-Type": "application/json" }, body: JSON.stringify(body) });
  return { app, repository, dispatch, rpc };
}

describe("shared-contract app RPC", () => {
  it("shares large-body admission across aliases and rejects overflow before reading its stream", async () => {
    const f = fixture({ scopes: ["mail.write"] }); let count = 0, entered!: () => void, release!: () => void;
    const ready = new Promise<void>((resolve) => { entered = resolve; }), blocked = new Promise<void>((resolve) => { release = resolve; });
    f.dispatch.mockImplementation(async () => { if (++count === 2) entered(); await blocked; return Response.json({}); });
    const body = JSON.stringify({ protocol: 2, method: "mail.drafts.create", params: { body: { connection_id: "connection_1", to: [], subject: "Fixture", text: "" } } });
    const headers = { Authorization: "Bearer app-token", "Content-Type": "application/json" };
    const requests = ["/app-runtime/rpc", "/v1/app-runtime/rpc"].map((path) => f.app.request(path, { method: "POST", headers, body }));
    await ready;
    const pull = vi.fn((controller: ReadableStreamDefaultController<Uint8Array>) => { controller.enqueue(new TextEncoder().encode(body)); controller.close(); });
    const stream = new ReadableStream<Uint8Array>({ pull }, { highWaterMark: 0 });
    try {
      const response = await f.app.request(new Request("http://fixture.invalid/api/app-runtime/rpc", { method: "POST", headers, body: stream, duplex: "half" } as RequestInit & { duplex: "half" }));
      expect(response.status).toBe(503); expect(await response.json()).toEqual({ code: "mail_capacity_unavailable" });
      expect(pull).not.toHaveBeenCalled(); expect(f.dispatch).toHaveBeenCalledTimes(2);
    } finally { release(); await stream.cancel(); await Promise.all(requests); }
    expect((await f.rpc(JSON.parse(body))).status).toBe(200);
  });
  it("supports only reviewed mail capabilities and requires explicit confirmation for sends", async () => {
    const { rpc, dispatch } = fixture({ scopes: ["mail.read", "mail.write"] });
    expect((await rpc({ protocol: 2, method: "mail.accounts.list" })).status).toBe(200);
    dispatch.mockClear();
    for (const body of [{ connection_id: "connection_1", authoring_source: "user", confirmed: false }, { connection_id: "connection_1", confirmed: true }]) {
      expect((await rpc({ protocol: 2, method: "mail.drafts.send", params: { path: { draftID: "draft_1" }, body } })).status).toBe(400);
    }
    expect(dispatch).not.toHaveBeenCalled();
    expect((await rpc({ protocol: 2, method: "mail.drafts.send", params: { path: { draftID: "draft_1" }, body: { connection_id: "connection_1", authoring_source: "ai", confirmed: true } } })).status).toBe(200);
    const readOnly = fixture({ scopes: ["mail.read"] });
    expect((await readOnly.rpc({ protocol: 2, method: "mail.threads.action", params: { path: { threadID: "thread_1" }, body: { connection_id: "connection_1", read: true } } })).status).toBe(403);
  });
  it("allows large bounded drafts only for mail writers and keeps ordinary envelopes and records capped", async () => {
    const write = fixture({ scopes: ["mail.write", "storage.write", "notes.write"] });
    const body = { connection_id: "connection_1", to: [{ email: "recipient@example.invalid" }], subject: "Test only", text: "x".repeat(5 * 1024 * 1024) };
    const envelope = { protocol: 2, method: "mail.drafts.create", params: { body } };
    expect((await write.rpc(envelope)).status).toBe(200);
    expect((await fixture({ scopes: ["mail.read"] }).rpc(envelope)).status).toBe(400);
    write.dispatch.mockClear();
    expect((await write.rpc({ protocol: 2, method: "notes.create", params: { body: { title: body.text } } })).status).toBe(400);
    expect((await write.app.request("/app-runtime/records/key", { method: "PUT", headers: { Authorization: "Bearer app-token", "Content-Type": "application/json" }, body: JSON.stringify({ data: body.text }) })).status).toBe(400);
    expect((await write.rpc({ ...envelope, params: { body: { ...body, text: "x".repeat(10 * 1024 * 1024 + 1) } } })).status).toBe(400);
    expect(write.dispatch).not.toHaveBeenCalled();
  });
  it("retains opaque mail IDs exactly through the real Hono router and query encoding", async () => {
    const domain = new Hono();
    domain.get("/mail/threads/:threadID", (c) => c.json({ id: c.req.param("threadID"), connection: c.req.query("connection_id") }));
    const base = fixture({ scopes: ["mail.read"] });
    const app = createApi({ logger: createLogger(loadRuntimeConfig("api", { LOG_LEVEL: "silent" })), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
      appRuntime: { repository: base.repository, dispatch: createNativeDispatcher([{ methods: new Set(["mail.threads.get"]), request: (request) => domain.request(request) }]) } });
    for (const id of ["a+b/c==", "%2F", "%25?x#y", "a/../b"]) {
      const response = await app.request("/app-runtime/rpc", { method: "POST", headers: { Authorization: "Bearer app-token", "Content-Type": "application/json" },
        body: JSON.stringify({ protocol: 2, method: "mail.threads.get", params: { path: { threadID: id }, query: { connection_id: "connection_1" } } }) });
      expect(response.status).toBe(200); expect(await response.json()).toEqual({ id, connection: "connection_1" });
    }
  });
  it("binds a valid named SDK method to the authenticated Space", async () => {
    const { rpc, dispatch } = fixture();
    const response = await rpc({ protocol: 2, method: "notes.list" });
    expect(response.status).toBe(200);
    expect(dispatch.mock.calls[0]?.[0]).toMatchObject({ method: "notes.list", params: { path: { spaceID: "space-1" } } });
  });
  it.each(["billing.checkout", "account.delete", "__proto__", "https://other.invalid"])("denies unsupported method %s before dispatch", async (method) => {
    const { rpc, dispatch } = fixture();
    expect((await rpc({ protocol: 2, method })).status).toBe(400);
    expect(dispatch).not.toHaveBeenCalled();
  });
  it("denies cross-Space requests before dispatch", async () => {
    const { rpc, dispatch } = fixture();
    const response = await rpc({ protocol: 2, method: "notes.list", params: { path: { spaceID: "space-2" } } });
    expect(await response.json()).toMatchObject({ code: "space_mismatch" });
    expect(dispatch).not.toHaveBeenCalled();
  });
  it.each(["connections.list", "integrations.list"] as const)("binds %s to membership and the connections grant", async (method) => {
    const { rpc, dispatch, repository } = fixture({ scopes: ["connections.read"] });
    expect((await rpc({ protocol: 2, method })).status).toBe(200);
    dispatch.mockClear();
    expect((await rpc({ protocol: 2, method, params: { path: { spaceID: "another-space" } } })).status).toBe(400);
    expect(dispatch).not.toHaveBeenCalled();
    vi.mocked(repository.isSpaceMember).mockResolvedValue(false);
    expect((await rpc({ protocol: 2, method })).status).toBe(403);
    expect(dispatch).not.toHaveBeenCalled();
  });
  it("denies writes when a session only grants read access", async () => {
    const { rpc, dispatch } = fixture();
    expect((await rpc({ protocol: 2, method: "notes.create", params: { body: { title: "Note" } } })).status).toBe(403);
    expect(dispatch).not.toHaveBeenCalled();
  });
  it("rechecks membership after validating the app capability", async () => {
    const { rpc, dispatch, repository } = fixture();
    vi.mocked(repository.isSpaceMember).mockResolvedValue(false);
    expect((await rpc({ protocol: 2, method: "notes.list" })).status).toBe(403);
    expect(dispatch).not.toHaveBeenCalled();
  });
  it("rejects expired app sessions", async () => {
    const { rpc, dispatch } = fixture({ expires_at: new Date(0) });
    expect((await rpc({ protocol: 2, method: "notes.list" })).status).toBe(401);
    expect(dispatch).not.toHaveBeenCalled();
  });
  it("does not accept an account cookie as an app credential", async () => {
    const { app, repository } = fixture();
    const response = await app.request("/v1/app-runtime/session", { headers: { Cookie: "misty_session=account-token" } });
    expect(response.status).toBe(401);
    expect(repository.findSession).not.toHaveBeenCalled();
  });
  it.each(["", "/api", "/v1"])("preserves session and record aliases at %s", async (prefix) => {
    const { app } = fixture();
    const headers = { Authorization: "Bearer app-token" };
    const response = await app.request(`${prefix}/app-runtime/session`, { headers });
    expect(response.status).toBe(200);
    const body = await response.json();
    expect(body).not.toHaveProperty("user_id");
    expect(body).not.toHaveProperty("token_hash");
    expect(body).toMatchObject({ app_id: "notes", space_id: "space-1" });
    expect((await app.request(`${prefix}/app-runtime/records`, { headers })).status).toBe(200);
  });
});
