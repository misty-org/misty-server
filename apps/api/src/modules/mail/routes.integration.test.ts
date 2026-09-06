import { randomUUID } from "node:crypto";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { pino } from "pino";
import { afterAll, afterEach, beforeAll, expect, it, vi } from "vitest";
import { parseMethodResult } from "@misty/contracts";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { createRequestBoundary } from "../../../../../packages/runtime/src/request-boundary.js";
import { createApi } from "../../app.js";
import { createAuthRepository } from "../auth/repository.js";
import { createAuthService, hashToken } from "../auth/service.js";
import { createPasswordHasher, type PasswordHasher } from "../auth/passwords.js";
import { createAppRuntimeRepository } from "../app-runtime/repository.js";
import { createOfficialCatalog } from "../official-apps/catalog.js";
import { createInstallationRepository } from "../official-apps/repository.js";
import { createSpaceRepository } from "../spaces/repository.js";
import { createConnectionRepository } from "../connections/repository.js";
import { createConnectionCipher } from "../connections/credentials.js";
import { createConnectionRevoker } from "../connections/revocation.js";
import { createConnectionTokenBroker } from "../connections/token-broker.js";
import { createOAuthTokenClient } from "../connections/oauth-token.js";

import { createMailService } from "./service.js";
import { createMailRepository } from "./repository.js";
import { createMailReader } from "./providers/reader.js";
import { createMailActionWriter } from "./providers/actions.js";
import { withDraftConnection } from "./draft-coordination.js";
import { createMailAudit } from "./audit.js";
import { createMailDraftWriter } from "./providers/drafts.js";
import { simpleParser } from "mailparser";

const threadFixtures = JSON.parse(await readFile(new URL("../../../../../docs/migration/fixtures/mail-threads.json", import.meta.url), "utf8")) as Array<{ provider: string; raw: Record<string, unknown> }>;

const admin = createTestDatabase(), users: string[] = [];
function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => { resolve = done; });
  return { promise, resolve };
}
let application: Pool, passwords: PasswordHasher;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_hono_app_test') THEN CREATE ROLE misty_hono_app_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON users,licenses,sessions,spaces,security_domains,space_members,space_roles,space_storage_usage,owner_storage_usage,
      space_setup_integrations,space_creation_requests,space_events,user_app_installations,app_runtime_sessions,app_install_events,app_data_deletion_jobs TO misty_hono_app_test;
    GRANT SELECT ON space_invitations TO misty_hono_app_test;
    GRANT SELECT,UPDATE ON connected_accounts,figma_webhook_subscriptions,figma_space_bindings,provider_shared_resources,space_integrations TO misty_hono_app_test;
    GRANT SELECT,INSERT ON mail_action_audit TO misty_hono_app_test;
    GRANT UPDATE(success,error_code,target_id,completed_at) ON mail_action_audit TO misty_hono_app_test;
    GRANT USAGE,SELECT ON app_install_events_id_seq,space_events_id_seq,mail_action_audit_id_seq TO misty_hono_app_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_app_test", max: 8 });
  passwords = await createPasswordHasher();
}, 60000);
afterEach(async () => {
  await withTransaction(admin, async (tx) => {
    await tx.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM space_creation_requests WHERE user_id=ANY($1::text[])", [users]);
    const domains = (await tx.query<{ security_domain_id: string }>("DELETE FROM spaces WHERE owner_user_id=ANY($1::text[]) RETURNING security_domain_id", [users])).rows.map((row) => row.security_domain_id);
    await tx.query("DELETE FROM security_domains WHERE id=ANY($1::text[])", [domains]);
    await tx.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
  }); users.length = 0;
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });

async function fixture() {
  const auth = createAuthService({ repository: createAuthRepository(application), passwords, deployment: "hosted" });
  const runtime = createAppRuntimeRepository(application), installations = createInstallationRepository(application);
  const cipher = createConnectionCipher(Buffer.alloc(32, 11));
  const provider = vi.fn<typeof fetch>(async () => new Response(null, { status: 200 }));
  const repository = createConnectionRepository(application, createConnectionRevoker(cipher, provider));
  const oauthProvider = vi.fn<typeof fetch>(async () => Response.json({ access_token: "native-refreshed-token", expires_in: 3600 }));
  const mailProvider = vi.fn<typeof fetch>(async (url) => {
    const path = new URL(String(url)).pathname;
    if (path.endsWith("/profile")) return Response.json({ emailAddress: "mail@example.invalid", messagesTotal: 12 });
    if (path.endsWith("/labels")) return Response.json({ labels: [{ id: "INBOX", name: "Inbox", type: "system", threadsTotal: 10, threadsUnread: 2 }, { id: "constructor", name: "Custom", type: "user" }, { id: "" }] });
    if (path.endsWith("/mailFolders")) return Response.json({ value: [{ id: "opaque+/=", displayName: "Sent &amp; saved", wellKnownName: "sentitems", totalItemCount: 4 }] });
    return Response.json({ id: "provider-user", displayName: "Mail &amp; account", mail: "outlook@example.invalid" });
  });
  const broker = createConnectionTokenBroker({ pool: application, cipher,
    refresh: createOAuthTokenClient({ google: { clientId: "fixture-id", clientSecret: "fixture-secret" }, microsoft: { clientId: "ms-id", clientSecret: "ms-secret" } }, oauthProvider).refresh });
  const app = createApi({ logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    auth: { service: auth, boundary: createRequestBoundary({ peerAddress: () => "127.0.0.1" }), deployment: "hosted" },
    mail: { auth, appRuntime: runtime, service: createMailService({ repository: createMailRepository(application), broker, reader: (lease) => createMailReader(lease, mailProvider), writer: (lease, guard) => createMailActionWriter(lease, guard, mailProvider), draftWriter: (lease, guard, created) => createMailDraftWriter(lease, guard, created, mailProvider) }) },
    connections: { auth, appRuntime: runtime, repository, providers: { google: true, microsoft: false } }, appRuntime: { repository: runtime } });
  const account = async () => {
    const username = `conn_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
    const result = await auth.register({ username, email: `${username}@example.invalid`, name: "Connection test", password: "test-password", analyticsEnabled: false }); users.push(result.user.id); return result;
  };
  const owner = await account(), other = await account();
  const { space } = await createSpaceRepository(application).create(owner.user.id, { name: "Connections", template_id: "blank", integration_providers: [] }, "");
  const request = (path: string, method = "GET", token = owner.token) => app.request(path, { method, headers: { Authorization: `Bearer ${token}` } });
  const rpc = (method: string, params: unknown, token: string) => app.request("/v1/app-runtime/rpc", { method: "POST", headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }, body: JSON.stringify({ protocol: 2, method, params }) });
  const connection = async (providerName = "google", userId = owner.user.id) => {
    const id = `connection_${randomUUID()}`, encrypted = cipher.encrypt(providerName, Buffer.from(JSON.stringify({ access_token: "test-only-private-token", refresh_token: "test-only-refresh" })));
    await admin.query(`INSERT INTO connected_accounts(id,user_id,provider,account_id,account_display,credential_ciphertext,credential_nonce,capabilities,granted_scopes)
      VALUES($1,$2,$3,$1,'Account',$4,$5,'["mail"]','["provider-private-scope"]')`, [id, userId, providerName, encrypted.ciphertext, encrypted.nonce]); return id;
  };
  const appToken = async (scopes = ["connections.read", "connections.write"]) => {
    await installations.install(owner.user.id, { ...createOfficialCatalog().find("terminal")!, scopes });
    const token = randomUUID(); await installations.session(owner.user.id, "terminal", hashToken(token), space.id); return token;
  };
  const state = async (id: string) => (await admin.query("SELECT status,revoked_at,octet_length(credential_ciphertext) AS bytes,octet_length(credential_nonce) AS nonce_bytes FROM connected_accounts WHERE id=$1", [id])).rows[0];
  return { app, owner, other, repository, installations, runtime, cipher, broker, spaceId: space.id, request, rpc, connection, appToken, state, provider, mailProvider, oauthProvider };
}

it("serves native Inbox accounts/folders, refreshes privately and preserves public DTOs through aliases and RPC", async () => {
  const f = await fixture(), google = await f.connection(), microsoft = await f.connection("microsoft"), hidden = await f.connection();
  await f.connection("google", f.other.user.id); await f.connection("figma");
  await admin.query("UPDATE connected_accounts SET capabilities='[]' WHERE id=$1", [hidden]);
  await admin.query("UPDATE connected_accounts SET expires_at=now()-interval '1 minute' WHERE id=$1", [google]);
  for (const prefix of ["", "/api", "/v1"]) {
    const response = await f.request(`${prefix}/mail/accounts`); expect(response.status, await response.clone().text()).toBe(200);
    const result = await response.json(); expect(() => parseMethodResult("mail.accounts.list", result)).not.toThrow();
    expect(result.accounts.map((account: { connection_id: string }) => account.connection_id)).toEqual([google, microsoft]);
    expect(result.accounts[0]).toMatchObject({ total: 12, unread: 0, email: "mail@example.invalid", display_name: "Account" });
    expect(result.accounts[1].display_name).toBe("Mail & account"); expect(result.accounts[0]).not.toHaveProperty("status");
    expect(JSON.stringify(result)).not.toMatch(/credential|nonce|token|secret/);
    const folders = await (await f.request(`${prefix}/mail/folders?connection_id=${google}`)).json();
    expect(() => parseMethodResult("mail.folders.list", folders)).not.toThrow();
    expect(folders.folders).toHaveLength(2); expect(folders.folders[0]).toMatchObject({ provider: "gmail", provider_id: "INBOX", kind: "inbox", total: 10, unread: 2, text_color: "", background: "" });
    expect(folders.folders[1].kind).toBe("custom");
  }
  expect(f.oauthProvider).toHaveBeenCalledTimes(1);
  const appToken = await f.appToken(["mail.read"]);
  const accounts = await f.rpc("mail.accounts.list", {}, appToken); expect(accounts.status).toBe(200);
  const folders = await f.rpc("mail.folders.list", { query: { connection_id: microsoft } }, appToken); expect(folders.status).toBe(200);
  expect((await folders.json()).folders[0]).toMatchObject({ provider_id: "opaque+/=", provider: "outlook", kind: "sent", name: "Sent & saved" });
  const graphRequest = f.mailProvider.mock.calls.find(([url]) => String(url).includes("graph.microsoft.com"))!;
  expect(new Headers(graphRequest[1]?.headers).get("Prefer")).toBe('IdType="ImmutableId"');
});

it("keeps Microsoft identities without mailboxes visible and sanitizes provider authorization errors", async () => {
  const f = await fixture(), google = await f.connection(), microsoft = await f.connection("microsoft");
  f.mailProvider.mockImplementation(async (url) => {
    const path = new URL(String(url)).pathname;
    if (path.endsWith("/mailFolders")) return Response.json({ error: { code: "MailboxNotEnabledForRESTAPI", message: "private provider detail" } }, { status: 404 });
    if (path.endsWith("/labels")) return Response.json({ error: { message: "private provider detail" } }, { status: 401 });
    if (path.endsWith("/profile")) return Response.json({ emailAddress: "mail@example.invalid" });
    return Response.json({ id: "provider-user", mail: "not-a-mailbox@example.invalid" });
  });
  const response = await f.request("/mail/accounts"); expect(response.status).toBe(200);
  const account = (await response.json()).accounts.find((row: { connection_id: string }) => row.connection_id === microsoft);
  expect(account).toMatchObject({ status: "needs_attention", error_code: "mail_provider_mailbox_unavailable", total: 0, unread: 0 });
  const folders = await f.request(`/mail/folders?connection_id=${google}`); expect(folders.status).toBe(424);
  expect(await folders.json()).toEqual({ code: "mail_provider_authorization_failed" });
  expect((await admin.query("SELECT status,last_error_code FROM connected_accounts WHERE id=$1", [google])).rows[0])
    .toEqual({ status: "needs_attention", last_error_code: "mail_provider_authorization_failed" });
  expect((await f.request(`/mail/folders?connection_id=${microsoft}`)).status).toBe(422);
});

it("requires mail.read, current membership and connection ownership without requiring connections.read", async () => {
  const f = await fixture(), own = await f.connection(), foreign = await f.connection("google", f.other.user.id);
  for (const scopes of [["mail.write"], ["connections.read"]]) {
    const token = await f.appToken(scopes); expect((await f.rpc("mail.accounts.list", {}, token)).status).toBe(403);
    expect((await f.request(`/mail/folders?connection_id=${own}`, "GET", token)).status).toBe(403);
  }
  const token = await f.appToken(["mail.read"]);
  expect((await f.rpc("mail.folders.list", { query: { connection_id: foreign } }, token)).status).toBe(404);
  expect((await f.request("/mail/folders", "GET", token)).status).toBe(400);
  expect(f.mailProvider).not.toHaveBeenCalled();
  expect((await f.rpc("mail.folders.list", { query: { connection_id: own } }, token)).status).toBe(200);
  await admin.query("DELETE FROM space_members WHERE space_id=$1 AND user_id=$2", [f.spaceId, f.owner.user.id]);
  expect((await f.rpc("mail.accounts.list", {}, token)).status).toBe(403);
  expect((await f.request("/mail/accounts", "GET", token)).status).toBe(403);
});

it("rechecks App access after a provider response and does not return folders after uninstall", async () => {
  const f = await fixture(), own = await f.connection(), token = await f.appToken(["mail.read"]), entered = deferred(), release = deferred();
  f.mailProvider.mockImplementation(async () => { entered.resolve(); await release.promise; return Response.json({ labels: [{ id: "INBOX", name: "Private" }] }); });
  const pending = f.rpc("mail.folders.list", { query: { connection_id: own } }, token);
  await entered.promise;
  try { await f.installations.uninstall(f.owner.user.id, "terminal"); } finally { release.resolve(); }
  const result = await pending; expect(result.status).toBe(401); expect(await result.text()).not.toContain("Private");
});

it("does not return folders after concurrent connection removal or follow an untrusted Graph next link", async () => {
  const f = await fixture(), own = await f.connection(), entered = deferred(), release = deferred();
  f.mailProvider.mockImplementation(async () => { entered.resolve(); await release.promise; return Response.json({ labels: [{ id: "INBOX", name: "Private" }] }); });
  const pending = f.request(`/mail/folders?connection_id=${own}`); await entered.promise;
  try { expect((await f.request(`/connections/${own}`, "DELETE")).status).toBe(204); } finally { release.resolve(); }
  const removed = await pending; expect(removed.status).toBe(404); expect(await removed.text()).not.toContain("Private");
  const microsoft = await f.connection("microsoft"); f.mailProvider.mockClear();
  f.mailProvider.mockImplementation(async () => Response.json({ value: [], "@odata.nextLink": "https://foreign.invalid/mail?$skiptoken=private" }));
  const response = await f.request(`/mail/folders?connection_id=${microsoft}`); expect(response.status).toBe(424);
  expect(await response.json()).toEqual({ code: "mail_provider_unavailable" }); expect(f.mailProvider).toHaveBeenCalledTimes(1);
});

it("serves Gmail thread reads through REST and RPC with exact opaque IDs and typed query filters", async () => {
  const f = await fixture(), connection = await f.connection(), token = await f.appToken(["mail.read"]); let currentId = "";
  f.mailProvider.mockImplementation(async (url) => {
    const target = new URL(String(url));
    if (target.pathname.endsWith("/threads")) return Response.json({ threads: [{ id: currentId }], nextPageToken: "next+/=", resultSizeEstimate: 10 });
    const id = decodeURIComponent(target.pathname.split("/").at(-1)!); expect(id).toBe(currentId);
    const raw = structuredClone(threadFixtures[0]!.raw); raw.id = id;
    for (const message of raw.messages as Array<Record<string, unknown>>) message.threadId = id;
    return Response.json(raw);
  });
  for (const id of ["a+b/c==", "%2F", "%25?x#y", "a/../b"]) {
    currentId = id;
    for (const prefix of ["", "/api", "/v1"]) {
      const response = await f.request(`${prefix}/mail/threads/${encodeURIComponent(id)}?connection_id=${connection}`); expect(response.status).toBe(200);
      const result = await response.json(); expect(() => parseMethodResult("mail.threads.get", result)).not.toThrow();
      expect(result.thread.provider_id).toBe(id); expect(result.thread.messages).toHaveLength(2);
      expect(result.thread.messages[1].attachments[0].provider_id).toBe("attach+/=");
    }
    const response = await f.rpc("mail.threads.get", { path: { threadID: id }, query: { connection_id: connection } }, token);
    expect(response.status).toBe(200); expect((await response.json()).thread.provider_id).toBe(id);
  }
  const page = await f.rpc("mail.threads.list", { query: { connection_id: connection, page_size: 25, page_token: "page+/=", folder_id: "label+/=", query: "from:a@example.invalid" } }, token);
  expect(page.status).toBe(200); const result = await page.json(); expect(() => parseMethodResult("mail.threads.list", result)).not.toThrow();
  expect(result).toMatchObject({ next_page_token: "next+/=", estimated_total: 10 });
  const request = f.mailProvider.mock.calls.map(([url]) => new URL(String(url))).find((url) => url.pathname.endsWith("/threads"))!;
  expect(Object.fromEntries(request.searchParams)).toEqual({ maxResults: "25", pageToken: "page+/=", labelIds: "label+/=", q: "from:a@example.invalid" });
});

it("paginates Graph conversations with immutable opaque IDs and nanosecond date precision", async () => {
  const f = await fixture(), connection = await f.connection("microsoft"), token = await f.appToken(["mail.read"]), id = "opaque'quote+/=";
  f.mailProvider.mockImplementation(async (url) => {
    const query = new URL(String(url)).searchParams; expect(query.get("$filter")).toBe("conversationId eq 'opaque''quote+/='");
    const rows = structuredClone(threadFixtures.find((entry) => entry.provider === "microsoft")!.raw.value) as Array<Record<string, unknown>>;
    for (const row of rows) row.conversationId = id;
    if (query.has("$skiptoken")) { expect(query.get("$skiptoken")).toBe("page+/="); return Response.json({ value: [rows[1]] }); }
    return Response.json({ value: [rows[0]], "@odata.nextLink": "https://graph.microsoft.com/v1.0/me/messages?$skiptoken=page%2B%2F%3D" });
  });
  const response = await f.rpc("mail.threads.get", { path: { threadID: id }, query: { connection_id: connection } }, token);
  expect(response.status, await response.clone().text()).toBe(200); const result = await response.json(); expect(() => parseMethodResult("mail.threads.get", result)).not.toThrow();
  expect(result.thread.provider_id).toBe(id); expect(result.thread.last_message_at).toBe("2026-09-05T12:00:00.123456789Z");
  expect(result.thread.messages.map((row: { provider_id: string }) => row.provider_id)).toEqual(["earlier", "later"]); expect(f.mailProvider).toHaveBeenCalledTimes(2);
});

it("validates mail-thread input and current scopes/ownership before provider access", async () => {
  const f = await fixture(), own = await f.connection(), foreign = await f.connection("google", f.other.user.id), token = await f.appToken(["mail.read"]);
  for (const query of ["page_size=0", "page_size=101", "page_size=2.5", `page_token=${"x".repeat(4097)}`, `query=${encodeURIComponent("雪".repeat(667))}`, `folder_id=${"x".repeat(321)}`])
    expect((await f.request(`/mail/threads?connection_id=${own}&${query}`)).status).toBe(400);
  expect((await f.rpc("mail.threads.get", { path: { threadID: "thread" }, query: { connection_id: foreign } }, token)).status).toBe(404);
  expect((await f.request("/mail/threads/thread")).status).toBe(400);
  const writer = await f.appToken(["mail.write"]);
  expect((await f.rpc("mail.threads.list", { query: { connection_id: own } }, writer)).status).toBe(403);
  expect((await f.request(`/mail/threads/thread?connection_id=${own}`, "GET", writer)).status).toBe(403); expect(f.mailProvider).not.toHaveBeenCalled();
});

it("does not hide metadata authorization failures and discards a thread after in-flight App revocation", async () => {
  const f = await fixture(), own = await f.connection(), token = await f.appToken(["mail.read"]);
  f.mailProvider.mockImplementation(async (url) => new URL(String(url)).pathname.endsWith("/threads") ? Response.json({ threads: [{ id: "thread" }] }) : Response.json({ error: { message: "private detail" } }, { status: 401 }));
  const failed = await f.rpc("mail.threads.list", { query: { connection_id: own } }, token); expect(failed.status).toBe(424);
  expect(await failed.json()).toEqual({ code: "mail_provider_authorization_failed" });
  expect((await admin.query("SELECT last_error_code FROM connected_accounts WHERE id=$1", [own])).rows[0].last_error_code).toBe("mail_provider_authorization_failed");
  const entered = deferred(), release = deferred();
  f.mailProvider.mockImplementation(async () => { entered.resolve(); await release.promise; return Response.json({ id: "thread", snippet: "Private message" }); });
  const pending = f.rpc("mail.threads.get", { path: { threadID: "thread" }, query: { connection_id: own } }, token);
  await entered.promise;
  try { await f.installations.uninstall(f.owner.user.id, "terminal"); } finally { release.resolve(); }
  const result = await pending; expect(result.status).toBe(401); expect(await result.text()).not.toContain("Private message");
});

it("commits a content-free action intent before Gmail writes and completes it through REST aliases and RPC", async () => {
  const f = await fixture(), connection = await f.connection(), token = await f.appToken(["mail.write"]), id = "%2F+/=";
  f.mailProvider.mockImplementation(async (url, options) => {
    expect(new URL(String(url)).pathname).toBe(`/gmail/v1/users/me/threads/${encodeURIComponent(id)}/modify`);
    expect(options!.method).toBe("POST"); expect(JSON.parse(options!.body as string)).toEqual({ addLabelIds: ["STARRED"], removeLabelIds: ["UNREAD", "INBOX"] });
    const pending = (await admin.query("SELECT action,target_id,source,confirmed,success,error_code,completed_at FROM mail_action_audit WHERE connection_id=$1 AND completed_at IS NULL", [connection])).rows;
    expect(pending).toEqual([{ action: "thread_modify", target_id: id, source: "user", confirmed: true, success: false, error_code: "mail_operation_pending", completed_at: null }]);
    return Response.json({ id });
  });
  const body = { connection_id: connection, read: true, archived: true, starred: true };
  for (const prefix of ["", "/api", "/v1"]) {
    const response = await f.app.request(`${prefix}/mail/threads/${encodeURIComponent(id)}/actions`, { method: "POST", headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }, body: JSON.stringify(body) });
    expect(response.status, await response.clone().text()).toBe(200); const result = await response.json(); expect(() => parseMethodResult("mail.threads.action", result)).not.toThrow();
  }
  const rpc = await f.rpc("mail.threads.action", { path: { threadID: id }, body }, token); expect(rpc.status).toBe(200);
  const rows = (await admin.query("SELECT success,error_code,completed_at FROM mail_action_audit WHERE connection_id=$1", [connection])).rows;
  expect(rows).toHaveLength(4); expect(rows.every((row) => row.success && row.error_code === "" && row.completed_at instanceof Date)).toBe(true);
  const legacy = await withTransaction(application, (tx) => tx.query(`INSERT INTO mail_action_audit(user_id,connection_id,action,target_type,target_id,source,confirmed,success,error_code)
    VALUES($1,$2,'thread_modify','thread','legacy','user',true,true,'') RETURNING completed_at`, [f.owner.user.id, connection]), { mode: "user", userId: f.owner.user.id });
  expect(legacy.rows[0].completed_at).toBeInstanceOf(Date);
  await expect(withTransaction(application, (tx) => tx.query("UPDATE mail_action_audit SET source='ai' WHERE connection_id=$1", [connection]), { mode: "service" })).rejects.toMatchObject({ code: "42501" });
});

it("rejects invalid actions, read-only apps and foreign connections before auditing or provider writes", async () => {
  const f = await fixture(), connection = await f.connection(), foreign = await f.connection("google", f.other.user.id), reader = await f.appToken(["mail.read"]);
  expect((await f.rpc("mail.threads.action", { path: { threadID: "thread" }, body: { connection_id: connection, read: true } }, reader)).status).toBe(403);
  const writer = await f.appToken(["mail.write"]);
  for (const body of [{ connection_id: connection }, { connection_id: connection, read: null }, { connection_id: connection, read: true, unexpected: "field" }]) {
    const response = await f.app.request("/mail/threads/thread/actions", { method: "POST", headers: { Authorization: `Bearer ${writer}` }, body: JSON.stringify(body) }); expect(response.status).toBe(400);
  }
  expect((await f.rpc("mail.threads.action", { path: { threadID: "thread" }, body: { connection_id: foreign, read: true } }, writer)).status).toBe(404);
  expect(f.mailProvider).not.toHaveBeenCalled(); expect((await admin.query("SELECT id FROM mail_action_audit WHERE user_id=$1", [f.owner.user.id])).rowCount).toBe(0);
});

it("records incomplete provider actions without retrying or exposing provider details", async () => {
  const f = await fixture(), connection = await f.connection(), token = await f.appToken(["mail.write"]);
  f.mailProvider.mockImplementation(async () => Response.json({ error: { message: "private mailbox detail" } }, { status: 429 }));
  const response = await f.rpc("mail.threads.action", { path: { threadID: "thread" }, body: { connection_id: connection, read: true } }, token);
  expect(response.status).toBe(429); expect(await response.json()).toEqual({ code: "mail_provider_rate_limited" }); expect(f.mailProvider).toHaveBeenCalledTimes(1);
  expect((await admin.query("SELECT success,error_code,completed_at IS NOT NULL AS completed FROM mail_action_audit WHERE connection_id=$1", [connection])).rows)
    .toEqual([{ success: false, error_code: "mail_operation_incomplete", completed: true }]);
});

it("serializes an in-flight Graph write with removal and stops before writing the next message", async () => {
  const f = await fixture(), connection = await f.connection("microsoft"), token = await f.appToken(["mail.write"]), entered = deferred(), release = deferred();
  let writes = 0;
  f.mailProvider.mockImplementation(async (_url, options) => {
    if (options!.method === "GET") return Response.json({ value: [{ id: "first", conversationId: "thread" }, { id: "second", conversationId: "thread" }] });
    writes++; entered.resolve(); await release.promise; return new Response(null, { status: 204 });
  });
  const pending = f.rpc("mail.threads.action", { path: { threadID: "thread" }, body: { connection_id: connection, read: true } }, token);
  await entered.promise; const removal = f.request(`/connections/${connection}`, "DELETE");
  try {
    await expect.poll(async () => Number((await admin.query("SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%connected_accounts%' AND pid<>pg_backend_pid()")).rows[0].count)).toBeGreaterThan(0);
  } finally { release.resolve(); }
  expect((await removal).status).toBe(204); expect((await pending).status).toBe(404); expect(writes).toBe(1);
  expect((await admin.query("SELECT success,error_code,completed_at IS NOT NULL AS completed FROM mail_action_audit WHERE connection_id=$1", [connection])).rows)
    .toEqual([{ success: false, error_code: "mail_operation_incomplete", completed: true }]);
});

it("fails closed when audit insertion fails and retains unfinished intent when completion cannot commit", async () => {
  const f = await fixture(), connection = await f.connection(), token = await f.appToken(["mail.write"]);
  await admin.query(`CREATE FUNCTION misty_test_reject_mail_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
    IF (TG_OP='INSERT' AND NEW.target_id='fixture_reject_insert') OR (TG_OP='UPDATE' AND NEW.target_id='fixture_reject_completion') THEN RAISE EXCEPTION 'fixture audit failure'; END IF;
    RETURN NEW; END $$;
    CREATE TRIGGER misty_test_reject_mail_audit BEFORE INSERT OR UPDATE ON mail_action_audit FOR EACH ROW EXECUTE FUNCTION misty_test_reject_mail_audit();`);
  f.mailProvider.mockImplementation(async () => new Response(null, { status: 204 }));
  try {
    for (const id of ["fixture_reject_insert", "fixture_reject_completion"]) {
      const response = await f.rpc("mail.threads.action", { path: { threadID: id }, body: { connection_id: connection, read: true } }, token); expect(response.status).toBe(500);
      expect(await response.text()).not.toContain("fixture audit failure");
      if (id.endsWith("insert")) expect(f.mailProvider).not.toHaveBeenCalled();
    }
    expect(f.mailProvider).toHaveBeenCalledTimes(1);
    expect((await admin.query("SELECT target_id,success,error_code,completed_at FROM mail_action_audit WHERE connection_id=$1", [connection])).rows)
      .toEqual([{ target_id: "fixture_reject_completion", success: false, error_code: "mail_operation_pending", completed_at: null }]);
  } finally { await admin.query("DROP TRIGGER misty_test_reject_mail_audit ON mail_action_audit; DROP FUNCTION misty_test_reject_mail_audit()"); }
});

it("coordinates each draft across connections and reuses one connection for audit and current-access transactions", async () => {
  const f = await fixture(), connection = await f.connection(), actor = { userId: f.owner.user.id }, signal = new AbortController().signal;
  const lease = await f.broker.acquire(actor, connection, "mailWrite", signal);
  const single = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_app_test", max: 1, connectionTimeoutMillis: 1000 });
  try {
    const entered = deferred(), release = deferred();
    const pending = withDraftConnection(single, connection, "draft+/=", signal, async (borrowed, current) => {
      const audit = createMailAudit(borrowed);
      const id = await audit.begin(actor, { connectionId: connection, action: "draft_update", targetType: "draft", targetId: "draft+/=", source: "user", confirmed: true });
      const first = await withTransaction(borrowed, async (tx) => (await tx.query("SELECT pg_backend_pid() AS pid")).rows[0].pid);
      await f.broker.whileCurrent(actor, lease, async () => { expect(current.aborted).toBe(false); }, borrowed);
      entered.resolve(); await release.promise;
      await audit.finish(actor.userId, id, true);
      const last = await withTransaction(borrowed, async (tx) => (await tx.query("SELECT pg_backend_pid() AS pid")).rows[0].pid);
      expect(last).toBe(first); return id;
    });
    await entered.promise;
    try {
      await expect(withDraftConnection(application, connection, "draft+/=", signal, async () => {})).rejects.toThrowError("mail_draft_busy");
      await expect(withDraftConnection(application, connection, "another-draft", signal, async () => "independent")).resolves.toBe("independent");
    } finally { release.resolve(); }
    await pending;
    await expect(withDraftConnection(single, connection, "draft+/=", signal, async () => "released")).resolves.toBe("released");
  } finally { await single.end(); }
});

it("preserves created draft identity and blocks unfinished, incomplete and already-sent audit histories", async () => {
  const f = await fixture(), connection = await f.connection(), actor = { userId: f.owner.user.id }, audit = createMailAudit(application);
  for (const state of ["unfinished", "incomplete", "sent", "edited"]) {
    const id = await audit.begin(actor, { connectionId: connection, action: state === "sent" ? "draft_send" : "draft_create", targetType: "draft", targetId: "new", source: "ai", confirmed: true });
    await audit.identifyDraft(actor.userId, id, state);
    if (state !== "unfinished") await audit.finish(actor.userId, id, state !== "incomplete", state === "incomplete" ? "mail_operation_incomplete" : "");
    const pending = audit.assertDraftSettled(actor.userId, connection, state);
    if (state === "edited") await expect(pending).resolves.toBeUndefined();
    else await expect(pending).rejects.toThrowError("mail_draft_reconciliation_required");
  }
  expect((await admin.query("SELECT target_id,source,confirmed FROM mail_action_audit WHERE connection_id=$1 ORDER BY id", [connection])).rows)
    .toEqual(["unfinished", "incomplete", "sent", "edited"].map((target_id) => ({ target_id, source: "ai", confirmed: true })));
});

it("aborts draft work if its database lock connection is lost and releases locks after failures", async () => {
  const signal = new AbortController().signal, key = randomUUID(); let pid = 0;
  const entered = deferred();
  const pending = withDraftConnection(application, key, "draft", signal, async (borrowed, current) => {
    pid = await withTransaction(borrowed, async (tx) => (await tx.query("SELECT pg_backend_pid() AS pid")).rows[0].pid);
    const aborted = new Promise<void>((resolve) => current.addEventListener("abort", () => resolve(), { once: true }));
    entered.resolve(); await aborted;
  });
  // Attach rejection handling before the separate connection kills the backend:
  // the client error may arrive before pg_terminate_backend's response does.
  const failure = pending.then(() => null, (error: unknown) => error);
  await entered.promise; await admin.query("SELECT pg_terminate_backend($1)", [pid]);
  expect(await failure).toMatchObject({ message: "mail_provider_unavailable" });
  await expect(withDraftConnection(application, key, "draft", signal, async () => { throw new Error("operation failed"); })).rejects.toThrowError("operation failed");
  await expect(withDraftConnection(application, key, "draft", signal, async () => "released")).resolves.toBe("released");
});

it("creates and edits Gmail drafts through all aliases and SDK RPC, preserving large attachments and opaque IDs", async () => {
  const f = await fixture(), connection = await f.connection(), token = await f.appToken(["mail.write"]);
  const attachment = Buffer.alloc(4 * 1024 * 1024, 37), seen: string[] = []; let next = 0;
  const drafts = new Map<string, { id: string; message: { id: string; threadId: string; labelIds: string[] } }>();
  f.mailProvider.mockImplementation(async (url, options) => {
    const target = new URL(String(url)), parts = target.pathname.split("/").map(decodeURIComponent);
    if (options!.method === "GET" && parts.at(-2) === "drafts") return Response.json(drafts.get(parts.at(-1)!));
    if (options!.method === "GET" && parts.at(-2) === "threads") return Response.json({ id: parts.at(-1), messages: [...drafts.values()].filter((draft) => draft.message.threadId === parts.at(-1)).map((draft) => draft.message) });
    expect(["POST", "PUT"]).toContain(options!.method); expect(target.pathname).not.toContain("/send");
    const body = JSON.parse(options!.body as string), mime = await simpleParser(Buffer.from(body.message.raw, "base64url"));
    seen.push(mime.subject!); expect(mime.bcc).toMatchObject({ value: [{ address: "hidden@example.invalid" }] });
    if (mime.attachments.length) expect(mime.attachments[0]!.content.equals(attachment)).toBe(true);
    const id = options!.method === "PUT" ? parts.at(-1)! : `draft_${++next}+/=%2F`;
    const draft = drafts.get(id) ?? { id, message: { id: `message_${next}`, threadId: `thread_${next}`, labelIds: ["DRAFT"] } }; drafts.set(id, draft); return Response.json(draft);
  });
  const body = { connection_id: connection, to: [{ email: "recipient@example.invalid" }], bcc: [{ email: "hidden@example.invalid" }], subject: "First", text: "Body" };
  for (const prefix of ["", "/api", "/v1"]) {
    const create = await f.app.request(`${prefix}/mail/drafts`, { method: "POST", headers: { Authorization: `Bearer ${token}` }, body: JSON.stringify(body) });
    expect(create.status, await create.clone().text()).toBe(201); const result = await create.json(); expect(() => parseMethodResult("mail.drafts.create", result)).not.toThrow();
    const update = await f.app.request(`${prefix}/mail/drafts/${encodeURIComponent(result.draft.provider_id)}`, { method: "PUT", headers: { Authorization: `Bearer ${token}` }, body: JSON.stringify({ ...body, subject: "Edited" }) });
    expect(update.status, await update.clone().text()).toBe(200); const edited = await update.json(); expect(() => parseMethodResult("mail.drafts.update", edited)).not.toThrow();
  }
  const create = await f.rpc("mail.drafts.create", { body: { ...body, attachments: [{ filename: "large.bin", content_type: "application/octet-stream", data: attachment.toString("base64"), inline: false }] } }, token);
  expect(create.status, await create.clone().text()).toBe(201); const result = await create.json();
  expect((await f.rpc("mail.drafts.update", { path: { draftID: result.draft.provider_id }, body: { ...body, subject: "Edited" } }, token)).status).toBe(200);
  expect(seen).toEqual(["First", "Edited", "First", "Edited", "First", "Edited", "First", "Edited"]);
  const rows = (await admin.query("SELECT success,error_code,target_id,completed_at IS NOT NULL AS completed FROM mail_action_audit WHERE connection_id=$1", [connection])).rows;
  expect(rows).toHaveLength(8); expect(rows.every((row) => row.success && row.completed && row.error_code === "" && row.target_id.startsWith("draft_"))).toBe(true);
});

it("preserves AI send provenance, requires confirmation and prevents replay after successful send", async () => {
  const f = await fixture(), connection = await f.connection(), token = await f.appToken(["mail.write"]), id = "draft+/=%2F";
  f.mailProvider.mockImplementation(async (url, options) => {
    if (options!.method === "GET") return Response.json({ id, message: { id: "message", threadId: "thread", labelIds: ["DRAFT"] } });
    expect(new URL(String(url)).pathname).toBe("/gmail/v1/users/me/drafts/send"); expect(JSON.parse(options!.body as string)).toEqual({ id });
    return Response.json({ id: "sent", threadId: "thread", labelIds: ["SENT"] });
  });
  const params = { path: { draftID: id }, body: { connection_id: connection, authoring_source: "ai", confirmed: false } };
  expect((await f.rpc("mail.drafts.send", params, token)).status).toBe(400);
  const direct = await f.app.request(`/mail/drafts/${encodeURIComponent(id)}/send`, { method: "POST", headers: { Authorization: `Bearer ${token}` }, body: JSON.stringify(params.body) });
  expect(direct.status).toBe(409); expect(await direct.json()).toEqual({ code: "mail_confirmation_required" }); expect(f.mailProvider).not.toHaveBeenCalled();
  const sent = await f.rpc("mail.drafts.send", { ...params, body: { ...params.body, confirmed: true } }, token);
  expect(sent.status, await sent.clone().text()).toBe(200); const result = await sent.json(); expect(() => parseMethodResult("mail.drafts.send", result)).not.toThrow();
  expect((await f.rpc("mail.drafts.send", { ...params, body: { ...params.body, confirmed: true } }, token)).status).toBe(409);
  expect(f.mailProvider).toHaveBeenCalledTimes(2);
  expect((await admin.query("SELECT source,confirmed,success,error_code FROM mail_action_audit WHERE connection_id=$1 ORDER BY id", [connection])).rows)
    .toEqual([{ source: "ai", confirmed: false, success: false, error_code: "mail_confirmation_required" }, { source: "ai", confirmed: true, success: true, error_code: "" }]);
});

it("rejects simultaneous draft edits and sends before a second provider operation", async () => {
  const f = await fixture(), connection = await f.connection(), token = await f.appToken(["mail.write"]), entered = deferred(), release = deferred();
  f.mailProvider.mockImplementation(async (url, options) => {
    const path = new URL(String(url)).pathname;
    if (options!.method === "GET" && path.endsWith("/drafts/draft")) { entered.resolve(); await release.promise; return Response.json({ id: "draft", message: { id: "message", threadId: "thread", labelIds: ["DRAFT"] } }); }
    if (path.endsWith("/threads/thread")) return Response.json({ id: "thread", messages: [{ id: "message", threadId: "thread", labelIds: ["DRAFT"] }] });
    expect(options!.method).toBe("PUT"); return Response.json({ id: "draft", message: { id: "message", threadId: "thread", labelIds: ["DRAFT"] } });
  });
  const edit = f.rpc("mail.drafts.update", { path: { draftID: "draft" }, body: { connection_id: connection, to: [], subject: "Edited", text: "" } }, token);
  await entered.promise;
  try {
    const send = await f.rpc("mail.drafts.send", { path: { draftID: "draft" }, body: { connection_id: connection, authoring_source: "user", confirmed: true } }, token);
    expect(send.status).toBe(409); expect(await send.json()).toEqual({ code: "mail_draft_busy" }); expect(f.mailProvider).toHaveBeenCalledTimes(1);
  } finally { release.resolve(); }
  expect((await edit).status).toBe(200);
  expect((await admin.query("SELECT action,success FROM mail_action_audit WHERE connection_id=$1", [connection])).rows).toEqual([{ action: "draft_update", success: true }]);
});

it("records an ambiguous draft send and refuses another attempt instead of risking duplicate delivery", async () => {
  const f = await fixture(), connection = await f.connection("microsoft"), token = await f.appToken(["mail.write"]);
  f.mailProvider.mockImplementation(async (_url, options) => {
    if (options!.method === "GET") return Response.json({ id: "draft", conversationId: "thread", isDraft: true });
    throw new Error("provider accepted bytes but response was lost");
  });
  const params = { path: { draftID: "draft" }, body: { connection_id: connection, authoring_source: "ai", confirmed: true } };
  expect((await f.rpc("mail.drafts.send", params, token)).status).toBe(424);
  expect((await f.rpc("mail.drafts.send", params, token)).status).toBe(409); expect(f.mailProvider).toHaveBeenCalledTimes(2);
  expect((await admin.query("SELECT source,confirmed,success,error_code FROM mail_action_audit WHERE connection_id=$1", [connection])).rows)
    .toEqual([{ source: "ai", confirmed: true, success: false, error_code: "mail_operation_incomplete" }]);
});

it("runs native Graph create, attachment upload, replacement and confirmed send through SDK RPC", async () => {
  const f = await fixture(), connection = await f.connection("microsoft"), token = await f.appToken(["mail.write"]), id = "draft+/=%2F";
  const bytes = Buffer.alloc(4 * 1024 * 1024, 19), uploaded: Buffer[] = []; let hasAttachment = false, draft = true;
  f.mailProvider.mockImplementation(async (url, options) => {
    const target = new URL(String(url)), path = target.pathname;
    if (target.origin === "https://outlook.office.com") {
      expect(new Headers(options!.headers).has("Authorization")).toBe(false); uploaded.push(Buffer.from(options!.body as Uint8Array));
      if (uploaded.length === 1) return Response.json({ nextExpectedRanges: ["2097152"] });
      hasAttachment = true; return new Response(null, { status: 201 });
    }
    if (path.endsWith("/createUploadSession")) {
      expect((await admin.query("SELECT target_id FROM mail_action_audit WHERE connection_id=$1 AND completed_at IS NULL", [connection])).rows).toEqual([{ target_id: id }]);
      return Response.json({ uploadUrl: "https://outlook.office.com/api/v2.0/Users('fixture')/Messages('draft')/AttachmentSessions('session')?authtoken=fixture-only",
        expirationDateTime: new Date(Date.now() + 600000).toISOString(), nextExpectedRanges: ["0-"] });
    }
    if (path.endsWith("/attachments") && options!.method === "GET") return Response.json({ value: hasAttachment ? [{ id: "attachment+/=" }] : [] });
    if (options!.method === "DELETE") { expect(path.endsWith("/attachments/attachment%2B%2F%3D")).toBe(true); hasAttachment = false; return new Response(null, { status: 204 }); }
    if (options!.method === "PATCH") expect(JSON.parse(options!.body as string)).toMatchObject({ bccRecipients: [], ccRecipients: [], replyTo: [] });
    if (path.endsWith("/send")) { expect(options!.body).toBeUndefined(); draft = false; return new Response(null, { status: 202 }); }
    return Response.json({ id, conversationId: "thread", isDraft: draft, body: { contentType: "Text", content: "Body" }, attachments: hasAttachment ? [{ id: "attachment+/=", name: "large.bin", size: bytes.length }] : [] });
  });
  const body = { connection_id: connection, to: [{ email: "recipient@example.invalid" }], subject: "Subject", text: "Body" };
  const created = await f.rpc("mail.drafts.create", { body: { ...body, attachments: [{ filename: "large.bin", content_type: "application/octet-stream", data: bytes.toString("base64"), inline: false }] } }, token);
  expect(created.status, await created.clone().text()).toBe(201); const result = await created.json(); expect(() => parseMethodResult("mail.drafts.create", result)).not.toThrow();
  expect(result.draft.message.attachments[0].size).toBe(bytes.length); expect(Buffer.concat(uploaded).equals(bytes)).toBe(true);
  const updated = await f.rpc("mail.drafts.update", { path: { draftID: id }, body }, token); expect(updated.status, await updated.clone().text()).toBe(200);
  expect((await updated.json()).draft.message.attachments).toEqual([]);
  const sent = await f.rpc("mail.drafts.send", { path: { draftID: id }, body: { connection_id: connection, authoring_source: "user", confirmed: true } }, token);
  expect(sent.status, await sent.clone().text()).toBe(200); expect((await sent.json()).message.draft).toBe(false);
  expect((await admin.query("SELECT action,success,target_id FROM mail_action_audit WHERE connection_id=$1 ORDER BY id", [connection])).rows)
    .toEqual(["draft_create", "draft_update", "draft_send"].map((action) => ({ action, success: true, target_id: id })));
});

it("blocks draft writes after App revocation during provider lookup and validates ownership before provider access", async () => {
  const f = await fixture(), connection = await f.connection("microsoft"), foreign = await f.connection("microsoft", f.other.user.id), token = await f.appToken(["mail.write"]);
  const body = { connection_id: foreign, to: [], subject: "Subject", text: "" };
  expect((await f.rpc("mail.drafts.create", { body }, token)).status).toBe(404); expect(f.mailProvider).not.toHaveBeenCalled();
  const entered = deferred(), release = deferred();
  f.mailProvider.mockImplementation(async (_url, options) => {
    expect(options!.method).toBe("GET"); entered.resolve(); await release.promise;
    return Response.json({ id: "draft", conversationId: "thread", isDraft: true });
  });
  const pending = f.rpc("mail.drafts.send", { path: { draftID: "draft" }, body: { connection_id: connection, authoring_source: "ai", confirmed: true } }, token);
  await entered.promise; try { await f.installations.uninstall(f.owner.user.id, "terminal"); } finally { release.resolve(); }
  expect((await pending).status).toBe(401); expect(f.mailProvider).toHaveBeenCalledTimes(1);
  expect((await admin.query("SELECT success,completed_at IS NOT NULL AS completed FROM mail_action_audit WHERE connection_id=$1", [connection])).rows).toEqual([{ success: false, completed: true }]);
});
