import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { pino } from "pino";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { createRequestBoundary } from "../../../../../packages/runtime/src/request-boundary.js";
import { createApi } from "../../app.js";
import { createAuthRepository } from "../auth/repository.js";
import { createAuthService, hashToken } from "../auth/service.js";
import { createPasswordHasher, type PasswordHasher } from "../auth/passwords.js";
import { AccountUnavailable, createAccountRepository } from "./repository.js";

const admin = createTestDatabase(), users: string[] = [];
let application: Pool, passwords: PasswordHasher;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_hono_app_test') THEN CREATE ROLE misty_hono_app_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON users,licenses,sessions TO misty_hono_app_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_app_test", max: 4 });
  passwords = await createPasswordHasher();
}, 60000);
afterEach(async () => { await admin.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]); users.length = 0; });
afterAll(async () => { if (application) await application.end(); await admin.end(); });
async function fixture() {
  const auth = createAuthService({ repository: createAuthRepository(application), passwords, deployment: "hosted" });
  const repository = createAccountRepository(application), boundary = createRequestBoundary({ peerAddress: () => "127.0.0.1" });
  const app = createApi({ logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    auth: { service: auth, boundary, deployment: "hosted" }, accounts: { auth, repository } });
  const username = `prefs_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
  const { user, token } = await auth.register({ username, email: `${username}@example.invalid`, name: "Preferences test", password: "test-password", analyticsEnabled: false });
  users.push(user.id);
  const put = (path: string, body: unknown, headers: Record<string, string> = {}) => app.request(path, {
    method: "PUT", body: JSON.stringify(body), headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json", ...headers },
  });
  const settings = () => app.request("/me/settings", { headers: { Authorization: `Bearer ${token}` } });
  return { app, user, token, repository, put, settings };
}

it("preserves preference replacement semantics and telemetry independence through all aliases", async () => {
  const f = await fixture();
  for (const prefix of ["", "/api", "/v1"]) {
    expect((await f.put(`${prefix}/me/settings`, { email_updates_enabled: true, analytics_enabled: true, error_reporting_enabled: true })).status).toBe(200);
    expect((await f.put(`${prefix}/me/telemetry`, { analytics_enabled: false })).status).toBe(200);
    const response = await f.settings(); expect(response.headers.get("Cache-Control")).toBe("no-store");
    expect(await response.json()).toEqual({ email_updates_enabled: true, analytics_enabled: false, error_reporting_enabled: false });
  }
  expect((await f.put("/me/settings", { analytics_enabled: null })).status).toBe(200);
  expect(await (await f.settings()).json()).toEqual({ email_updates_enabled: false, analytics_enabled: false, error_reporting_enabled: false });
});

it("updates only the authenticated profile and device while preserving identity and license state", async () => {
  const first = await fixture(), second = await fixture();
  expect((await first.put("/v1/me/profile", { name: "  Renamed 🙂  " })).status).toBe(200);
  expect((await first.put("/api/me/device", { device: "  workstation  " })).status).toBe(200);
  const row = (await admin.query("SELECT u.name,u.email,u.username,l.tier,l.status,l.license_device FROM users u JOIN licenses l ON l.id=u.license_id WHERE u.id=$1", [first.user.id])).rows[0];
  expect(row).toEqual({ name: "Renamed 🙂", email: first.user.email, username: first.user.username, tier: "basic", status: "active", license_device: "  workstation  " });
  expect((await admin.query("SELECT name FROM users WHERE id=$1", [second.user.id])).rows[0].name).toBe("Preferences test");
  expect((await first.put("/me/device", {})).status).toBe(200);
  expect((await admin.query("SELECT license_device FROM licenses WHERE user_id=$1", [first.user.id])).rows[0].license_device).toBe("");
});

it("rejects invalid bodies and untrusted origins without altering stored preferences", async () => {
  const f = await fixture();
  for (const body of [{ analytics_enabled: "true" }, { analytics_enabled: true, user_id: "another-user" }, { unknown: true }, []])
    expect((await f.put("/me/settings", body)).status).toBe(400);
  expect((await f.put("/me/profile", { name: " " })).status).toBe(400);
  expect((await f.put("/me/profile", { name: "x".repeat(8193) })).status).toBe(400);
  expect((await f.put("/me/settings", { analytics_enabled: true }, { Origin: "https://untrusted.invalid" })).status).toBe(403);
  expect((await f.app.request("/me/settings", { method: "PUT", headers: { Authorization: `Bearer ${f.token}` }, body: '{}{}' })).status).toBe(400);
  expect((await f.repository.settings(f.user.id)).analytics_enabled).toBe(false);
});

it("rejects app credentials and stops updates after account deactivation", async () => {
  const f = await fixture();
  await admin.query("INSERT INTO user_app_installations(user_id,app_id,installed_version,granted_scopes) VALUES($1,'test','1.0.0','[]')", [f.user.id]);
  await admin.query("INSERT INTO app_runtime_sessions(token_hash,user_id,app_id,scopes,expires_at) VALUES($1,$2,'test','[]',now()+interval '5 minutes')", [hashToken("prefs-app-token"), f.user.id]);
  expect((await f.put("/me/settings", {}, { Authorization: "Bearer prefs-app-token" })).status).toBe(401);
  await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [f.user.id]);
  expect((await f.put("/me/profile", { name: "revived" })).status).toBe(401);
  await expect(f.repository.name(f.user.id, "revived")).rejects.toBeInstanceOf(AccountUnavailable);
  await expect(f.repository.device(f.user.id, "revived")).rejects.toBeInstanceOf(AccountUnavailable);
  expect((await admin.query("SELECT name FROM users WHERE id=$1", [f.user.id])).rows[0].name).toBe("Preferences test");
});
