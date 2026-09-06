import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { pino } from "pino";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { createRequestBoundary } from "../../../../../packages/runtime/src/request-boundary.js";
import { createApi } from "../../app.js";
import { createAuthRepository } from "../auth/repository.js";
import { createAuthService, hashToken } from "../auth/service.js";
import { createPasswordHasher, type PasswordHasher } from "../auth/passwords.js";
import { createAppRuntimeRepository } from "../app-runtime/repository.js";
import { createOfficialCatalog } from "./catalog.js";
import { createAppPurgeJobs, createAppPurgeRepository } from "./purge-jobs.js";
import { createInstallationRepository } from "./repository.js";

const admin = createTestDatabase(), users: string[] = [], spaces: string[] = [], domains: string[] = [];
let application: Pool, passwords: PasswordHasher;
const catalog = createOfficialCatalog(), terminal = catalog.find("terminal")!;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_hono_app_test') THEN CREATE ROLE misty_hono_app_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON users,licenses,sessions,user_app_installations,app_runtime_sessions,app_personal_records,app_data_deletion_jobs,app_install_events,user_app_activity TO misty_hono_app_test;
    GRANT SELECT ON spaces,space_members TO misty_hono_app_test;
    GRANT USAGE,SELECT ON app_install_events_id_seq TO misty_hono_app_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_app_test", max: 8 });
  passwords = await createPasswordHasher();
}, 60000);
afterEach(async () => {
  await withTransaction(admin, async (tx) => {
    await tx.query("DELETE FROM spaces WHERE id=ANY($1::text[])", [spaces]);
    await tx.query("DELETE FROM security_domains WHERE id=ANY($1::text[])", [domains]);
    await tx.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
  });
  spaces.length = 0; domains.length = 0; users.length = 0;
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });
async function fixture(customCatalog = catalog) {
  let now = new Date();
  const auth = createAuthService({ repository: createAuthRepository(application), passwords, deployment: "hosted" });
  const repository = createInstallationRepository(application, () => now), runtime = createAppRuntimeRepository(application);
  const boundary = createRequestBoundary({ peerAddress: () => "127.0.0.1" });
  const app = createApi({ logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    auth: { service: auth, boundary, deployment: "hosted" }, officialApps: { auth, repository, catalog: customCatalog }, appRuntime: { repository: runtime } });
  const username = `install_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
  const { user, token } = await auth.register({ username, email: `${username}@example.invalid`, name: "Installation test", password: "test-password", analyticsEnabled: false });
  users.push(user.id);
  const request = (path: string, method = "GET", body?: unknown, credential = token) => app.request(path, {
    method, ...(body !== undefined ? { body: JSON.stringify(body) } : {}), headers: { Authorization: `Bearer ${credential}`, "Content-Type": "application/json" },
  });
  const install = () => request("/v1/me/apps/terminal", "PUT", { permission_version: terminal.permission_version });
  const session = async () => {
    const response = await request("/v1/me/apps/terminal/sessions", "POST", {});
    expect(response.status, await response.clone().text()).toBe(201); return response.json();
  };
  const space = async () => {
    const id = randomUUID(), domain = randomUUID();
    await withTransaction(admin, async (tx) => {
      await tx.query("INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES($1,'space',$2,$3)", [domain, user.id, id]);
      await tx.query("INSERT INTO spaces(id,owner_user_id,name,security_domain_id) VALUES($1,$2,'Install test',$3)", [id, user.id, domain]);
      await tx.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'owner')", [id, user.id]);
    });
    domains.push(domain); spaces.push(id); return id;
  };
  return { app, user, token, repository, runtime, request, install, session, space, setTime: (time: Date) => { now = time; } };
}

it("serves the reviewed catalog and account installation list under all aliases without exposing signing metadata", async () => {
  const f = await fixture();
  for (const prefix of ["", "/api", "/v1"]) {
    const response = await f.request(`${prefix}/apps`);
    expect(response.status).toBe(200); expect(response.headers.get("Cache-Control")).toBe("no-store");
    expect(await response.json()).toEqual({ apps: catalog.all(), host_protocol_version: catalog.hostProtocol });
    expect(await (await f.request(`${prefix}/apps/TERMINAL`)).json()).toEqual(terminal);
    expect(await (await f.request(`${prefix}/me/apps`)).json()).toEqual({ apps: [] });
  }
  expect((await f.request("/apps/unknown")).status).toBe(404);
  expect((await f.request("/apps", "GET", undefined, "invalid")).status).toBe(401);
  const browser = catalog.find("browser")!;
  expect(browser).toMatchObject({ version: "1.1.0", permission_version: 2, minimum_host_protocol: 2, desktop: { runtime: "downloaded" }, mobile: { runtime: "embedded" } });
  expect(browser.desktop.sha256).toMatch(/^[a-f0-9]{64}$/); expect(browser.desktop.signature).toBeTruthy();
  expect((await f.request("/me/apps/browser", "PUT", { permission_version: 1 })).status).toBe(409);
  expect((await f.request("/me/apps/browser", "PUT", { permission_version: 2 })).status).toBe(200);
  const session = await f.request("/me/apps/browser/sessions", "POST", {}); expect(session.status).toBe(201);
  expect((await session.json()).scopes).toEqual(["spaces.read", "browser.navigate", "browser.inspect", "browser.interact", "clipboard.write", "links.open", "navigation.write", "ai.use"]);
  const inbox = catalog.find("inbox")!;
  expect(inbox).toMatchObject({ version: "1.1.0", permission_version: 3, minimum_host_protocol: 2,
    desktop: { runtime: "downloaded", sha256: "a0e238d51e408427c4d2d9a4dc5c14991965bdcf39a82ffc8de630ea9a61cc4e" } });
  expect((await f.request("/me/apps/inbox", "PUT", { permission_version: 2 })).status).toBe(409);
  expect((await f.request("/me/apps/inbox", "PUT", { permission_version: 3 })).status).toBe(200);
  const inboxSession = await f.request("/me/apps/inbox/sessions", "POST", {}); expect(inboxSession.status).toBe(201);
  expect((await inboxSession.json()).scopes).toEqual(inbox.scopes);
});

it("requires reviewed permissions, rejects caller-supplied scopes and issues hashed short-lived app credentials", async () => {
  const f = await fixture();
  expect((await f.request("/me/apps/terminal", "PUT", { permission_version: terminal.permission_version - 1 })).status).toBe(409);
  expect((await f.request("/me/apps/terminal", "PUT", { permission_version: terminal.permission_version, scopes: ["admin"] })).status).toBe(400);
  expect((await f.install()).status).toBe(200);
  const session = await f.session();
  expect(session).toMatchObject({ app_id: "terminal", space_id: "", scopes: terminal.scopes, sdk_base_url: "/v1/app-runtime" });
  expect(session.token).toMatch(/^[A-Za-z0-9_-]{43}$/);
  const stored = (await admin.query("SELECT token_hash,expires_at FROM app_runtime_sessions WHERE user_id=$1", [f.user.id])).rows[0];
  expect(stored.token_hash).toBe(hashToken(session.token)); expect(stored.expires_at.toISOString()).toBe(session.expires_at);
  expect((await f.request("/app-runtime/session", "GET", undefined, session.token)).status).toBe(200);
  expect((await f.request("/me/apps", "GET", undefined, session.token)).status).toBe(401);
  expect((await f.request("/me/apps/journal/sessions", "POST", {})).status).toBe(409);
});

it("binds credentials to active Space membership and isolates other accounts' installations", async () => {
  const first = await fixture(), second = await fixture();
  await first.install(); const spaceId = await first.space();
  expect((await first.request("/me/apps/terminal/sessions", "POST", { space_id: spaceId })).status).toBe(201);
  expect((await first.request("/me/apps/terminal/sessions", "POST", { space_id: "missing-space" })).status).toBe(403);
  await second.install();
  expect((await second.request("/me/apps/terminal/sessions", "POST", { space_id: spaceId })).status).toBe(403);
  await admin.query("UPDATE spaces SET lifecycle_state='pending_deletion' WHERE id=$1", [spaceId]);
  expect((await first.request("/me/apps/terminal/sessions", "POST", { space_id: spaceId })).status).toBe(403);
  await first.request("/me/apps/terminal", "DELETE");
  expect((await second.repository.list(second.user.id))[0]?.state).toBe("installed");
});

it("revokes old grants on reviewed upgrade and cannot revive credentials after uninstall/reinstall", async () => {
  const f = await fixture();
  await f.repository.install(f.user.id, { ...terminal, version: "0.9.0", permission_version: terminal.permission_version - 1, scopes: ["spaces.read"] });
  const old = await f.session();
  expect((await f.request("/me/apps/terminal", "PUT", { permission_version: terminal.permission_version - 1 })).status).toBe(409);
  expect(await f.runtime.findSession(hashToken(old.token))).not.toBeNull();
  await f.install(); expect(await f.runtime.findSession(hashToken(old.token))).toBeNull();
  const fresh = await f.session();
  const authenticatedBeforeUninstall = await f.runtime.findSession(hashToken(fresh.token));
  const removed = await (await f.request("/me/apps/terminal", "DELETE")).json();
  expect(removed.state).toBe("recoverable");
  expect(new Date(removed.data_deletion_at).getTime() - new Date(removed.uninstalled_at).getTime()).toBe(30 * 86400_000);
  f.setTime(new Date(Date.now() + 86400_000));
  expect(await (await f.request("/me/apps/terminal", "DELETE")).json()).toEqual(removed);
  expect(await f.runtime.findSession(hashToken(fresh.token))).toBeNull();
  await f.install(); expect(await f.runtime.findSession(hashToken(fresh.token))).toBeNull();
  await expect(f.runtime.putRecord(authenticatedBeforeUninstall!, "stale-request", { text: "must not revive" })).rejects.toThrow();
  expect((await admin.query("SELECT count(*) FROM app_personal_records WHERE user_id=$1 AND record_key='stale-request'", [f.user.id])).rows[0].count).toBe("0");
  expect((await admin.query("SELECT count(*) FROM app_data_deletion_jobs WHERE user_id=$1", [f.user.id])).rows[0].count).toBe("0");
});

it("preserves recoverable private data and pin ordering, and refuses restoration once purging starts", async () => {
  const f = await fixture(); await f.install();
  await admin.query("INSERT INTO app_personal_records(user_id,app_id,record_key,data) VALUES($1,'terminal','draft','{\"text\":\"keep\"}')", [f.user.id]);
  expect((await f.request("/me/apps/terminal", "PATCH", { pinned: false })).status).toBe(200);
  expect((await f.request("/me/apps/terminal", "PATCH", {})).status).toBe(400);
  await f.install(); expect((await f.repository.list(f.user.id))[0]?.pinned).toBe(false);
  await f.request("/me/apps/terminal", "DELETE"); await f.install();
  expect((await f.repository.list(f.user.id))[0]?.pinned).toBe(true);
  expect((await admin.query("SELECT data FROM app_personal_records WHERE user_id=$1", [f.user.id])).rows[0].data).toEqual({ text: "keep" });
  await f.request("/me/apps/terminal", "DELETE");
  await admin.query("UPDATE user_app_installations SET state='purging' WHERE user_id=$1", [f.user.id]);
  expect((await f.install()).status).toBe(409);
  expect((await f.request("/me/apps/terminal", "DELETE")).status).toBe(409);
});

it("rolls back installation, credentials and purge scheduling together when the audit event fails", async () => {
  const f = await fixture(); await f.install(); const session = await f.session();
  await admin.query("REVOKE INSERT ON app_install_events FROM misty_hono_app_test");
  try { expect((await f.request("/me/apps/terminal", "DELETE")).status).toBe(500); }
  finally { await admin.query("GRANT INSERT ON app_install_events TO misty_hono_app_test"); }
  expect((await f.repository.list(f.user.id))[0]?.state).toBe("installed");
  expect(await f.runtime.findSession(hashToken(session.token))).not.toBeNull();
  expect((await admin.query("SELECT count(*) FROM app_data_deletion_jobs WHERE user_id=$1", [f.user.id])).rows[0].count).toBe("0");
});

it("serializes simultaneous installs and rejects session issuance after account deletion begins", async () => {
  const f = await fixture();
  const responses = await Promise.all(Array.from({ length: 6 }, () => f.install()));
  expect(responses.map((response) => response.status)).toEqual(Array(6).fill(200));
  expect((await f.repository.list(f.user.id))).toHaveLength(1);
  await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [f.user.id]);
  expect((await f.request("/me/apps/terminal/sessions", "POST", {})).status).toBe(401);
  await expect(f.repository.session(f.user.id, "terminal", hashToken("post-delete"), "")).rejects.toThrow("account_unavailable");
});

async function purgeFixture() {
  const f = await fixture(); await f.install(); await f.repository.install(f.user.id, catalog.find("journal")!);
  await admin.query("INSERT INTO app_personal_records(user_id,app_id,record_key,data) VALUES($1,'terminal','draft','{}'),($1,'journal','other-app','{}')", [f.user.id]);
  await f.request("/me/apps/terminal", "DELETE");
  const mature = () => admin.query("UPDATE app_data_deletion_jobs SET delete_at=now()-interval '1 second' WHERE user_id=$1 AND app_id='terminal'", [f.user.id]);
  const count = async () => (await admin.query("SELECT count(*) FROM app_personal_records WHERE user_id=$1 AND app_id='terminal'", [f.user.id])).rows[0].count;
  return { ...f, mature, count };
}

it("purges only private app data after recovery expires and permits clean installation afterward", async () => {
  const f = await purgeFixture(), other = await fixture(); await other.install();
  await admin.query("INSERT INTO app_personal_records(user_id,app_id,record_key,data) VALUES($1,'terminal','other-user','{}')", [other.user.id]);
  const spaceId = await f.space(), noteId = randomUUID();
  await admin.query("INSERT INTO space_notes(id,space_id,creator_user_id) VALUES($1,$2,$3)", [noteId, spaceId, f.user.id]);
  await admin.query("INSERT INTO user_app_activity(user_id,app_id) VALUES($1,'terminal'),($1,'journal')", [f.user.id]);
  const jobs = createAppPurgeJobs(application);
  await jobs.runOnce(); expect(await f.count()).toBe("1");
  await f.mature(); await jobs.runOnce();
  expect(await f.count()).toBe("0");
  expect((await admin.query("SELECT state FROM app_data_deletion_jobs WHERE user_id=$1", [f.user.id])).rows[0].state).toBe("completed");
  expect((await f.repository.list(f.user.id)).map((item) => item.app_id)).toEqual(["journal"]);
  expect((await admin.query("SELECT count(*) FROM app_personal_records WHERE (user_id=$1 AND app_id='journal') OR user_id=$2", [f.user.id, other.user.id])).rows[0].count).toBe("2");
  expect((await admin.query("SELECT app_id FROM user_app_activity WHERE user_id=$1", [f.user.id])).rows).toEqual([{ app_id: "journal" }]);
  expect((await admin.query("SELECT id FROM space_notes WHERE id=$1", [noteId])).rowCount).toBe(1);
  expect((await f.install()).status).toBe(200); expect(await f.count()).toBe("0");
  expect((await admin.query("SELECT count(*) FROM app_data_deletion_jobs WHERE user_id=$1", [f.user.id])).rows[0].count).toBe("0");
});

it("serializes claims with restoration and fences stale purge completion and failure after reclaim", async () => {
  const f = await purgeFixture(); await f.mature();
  const repository = createAppPurgeRepository(application);
  const claims = (await Promise.all([repository.claim(), repository.claim()])).flat(); expect(claims).toHaveLength(1);
  expect((await f.install()).status).toBe(409);
  await admin.query("UPDATE app_data_deletion_jobs SET started_at=now()-interval '16 minutes' WHERE user_id=$1", [f.user.id]);
  const reclaimed = await repository.claim(); expect(reclaimed).toHaveLength(1);
  expect(await repository.complete(claims[0]!)).toBe(false); expect(await repository.fail(claims[0]!)).toBe(false);
  expect(await f.count()).toBe("1");
  expect(await repository.complete(reclaimed[0]!)).toBe(true);
  expect(await repository.complete(reclaimed[0]!)).toBe(false);
  expect(await f.count()).toBe("0");
  const restored = await purgeFixture(); await restored.mature();
  // Whichever operation wins the installation lock defines the outcome. A
  // restored installation is never purged by an earlier candidate snapshot.
  const [response, raceClaims] = await Promise.all([restored.install(), repository.claim()]);
  if (response.status === 200) expect(raceClaims).toHaveLength(0);
  else { expect(response.status).toBe(409); expect(raceClaims).toHaveLength(1); await repository.complete(raceClaims[0]!); }
});

it("rolls back private data deletion on audit failure and backs off before a safe retry", async () => {
  const f = await purgeFixture(); await f.mature();
  const repository = createAppPurgeRepository(application), claims = await repository.claim(); expect(claims).toHaveLength(1);
  await admin.query("REVOKE INSERT ON app_install_events FROM misty_hono_app_test");
  try { await expect(repository.complete(claims[0]!)).rejects.toThrow(); }
  finally { await admin.query("GRANT INSERT ON app_install_events TO misty_hono_app_test"); }
  expect(await f.count()).toBe("1");
  expect((await f.repository.list(f.user.id)).find((item) => item.app_id === "terminal")?.state).toBe("purging");
  await repository.fail(claims[0]!);
  expect((await f.repository.list(f.user.id)).find((item) => item.app_id === "terminal")?.state).toBe("recoverable");
  expect(await repository.claim()).toEqual([]);
  expect((await admin.query("SELECT state,last_error FROM app_data_deletion_jobs WHERE user_id=$1", [f.user.id])).rows[0]).toEqual({ state: "failed", last_error: "app_data_purge_failed" });
  await admin.query("UPDATE app_data_deletion_jobs SET updated_at=now()-interval '2 hours' WHERE user_id=$1", [f.user.id]);
  await createAppPurgeJobs(application).runOnce(); expect(await f.count()).toBe("0");
});
