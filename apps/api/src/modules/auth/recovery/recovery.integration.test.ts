import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { pino } from "pino";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../../packages/database/src/test-database.js";
import { createRequestBoundary } from "../../../../../../packages/runtime/src/request-boundary.js";
import type { EmailMessage, EmailSender } from "../../../../../../packages/email/src/mailjet.js";
import { createApi } from "../../../app.js";
import { createAuthRepository } from "../repository.js";
import { createAuthService, hashToken } from "../service.js";
import { createPasswordHasher, type PasswordHasher } from "../passwords.js";
import { createRecoveryRepository } from "./repository.js";
import { createRecoveryService } from "./service.js";
import { createRecoveryJobs } from "./jobs.js";
import type { RecoveryTokenKeys } from "./token-keys.js";
import { createAuthCleanup } from "../cleanup.js";

const admin = createTestDatabase(), emails: string[] = [], jobEmails = new Set<string>();
let application: Pool, passwords: PasswordHasher;
const start = new Date("2026-09-05T12:00:00Z");
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_hono_app_test') THEN CREATE ROLE misty_hono_app_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON users,licenses,sessions,password_reset_tokens,app_runtime_sessions,auth_handoff_tokens,password_recovery_jobs TO misty_hono_app_test;
    GRANT SELECT,UPDATE,DELETE ON connection_authorization_requests,connected_account_oauth_states TO misty_hono_app_test;
    GRANT USAGE ON SEQUENCE password_recovery_jobs_request_order_seq TO misty_hono_app_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_app_test", max: 4 });
  passwords = await createPasswordHasher();
}, 60000);
afterEach(async () => { await admin.query("DELETE FROM password_recovery_jobs WHERE email=ANY($1::text[])", [[...jobEmails]]); jobEmails.clear(); await admin.query("DELETE FROM users WHERE email=ANY($1::text[])", [emails]); emails.length = 0; });
afterAll(async () => { if (application) await application.end(); await admin.end(); });

function fixture(send?: EmailSender) {
  let now = start;
  const messages: EmailMessage[] = [], logs: string[] = [];
  const logger = pino({ level: "warn" }, { write: (chunk: string) => { logs.push(chunk); } });
  const repository = createRecoveryRepository(application);
  const config = { startUrl: "https://api.example.invalid/v1/auth/reset/start", redirectUrl: "https://apps.mistysys.com/#/reset" };
  const makeJobs = (delivery: EmailSender = send ?? (async (message) => { messages.push(message); }), keys: RecoveryTokenKeys = { active: "test", keys: new Map([["test", Buffer.alloc(32, 17)]]) }) =>
    createRecoveryJobs({ pool: application, keys, logger, now: () => now, config, send: delivery });
  const jobs = makeJobs();
  const recovery = createRecoveryService({ repository, passwords, logger, now: () => now, config, jobs });
  const auth = createAuthService({ repository: createAuthRepository(application), passwords, deployment: "hosted", now: () => now });
  const boundary = createRequestBoundary({ peerAddress: () => "127.0.0.1" });
  const app = createApi({ logger, checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    auth: { service: auth, recovery, boundary, deployment: "hosted", now: () => now.getTime() } });
  const post = (path: string, body: unknown, headers: Record<string, string> = {}) => {
    if (path.endsWith("/auth/forgot") && typeof (body as { email?: unknown })?.email === "string") jobEmails.add((body as { email: string }).email.trim().toLowerCase());
    return app.request(path, {
    method: "POST", body: JSON.stringify(body), headers: { "Content-Type": "application/json", ...headers },
    });
  };
  const register = async () => {
    const username = `reset_${randomUUID().replaceAll("-", "").slice(0, 12)}`, email = `${username}@example.invalid`;
    emails.push(email);
    const response = await post("/register", { username, email, name: "Reset test", password: "old-test-password" });
    expect(response.status).toBe(201);
    return { ...await response.json(), email } as { user_id: string; token: string; email: string };
  };
  const issue = async (email: string) => {
    expect((await post("/api/auth/forgot", { email })).status).toBe(202);
    await jobs.runOnce();
    const link = messages.at(-1)!.text.split("\n").find((line) => line.startsWith("https://"))!;
    return { link, token: new URL(link).searchParams.get("token")! };
  };
  return { app, auth, recovery, repository, jobs, makeJobs, messages, logs, post, register, issue, setTime: (time: Date) => { now = time; } };
}

it("preserves reset links/cookies and atomically revokes all sessions and handoffs", async () => {
  const f = fixture(), account = await f.register(), other = await f.register();
  await admin.query("INSERT INTO user_app_installations(user_id,app_id,installed_version,granted_scopes) VALUES($1,'reset-test','1.0.0','[]')", [account.user_id]);
  await admin.query("INSERT INTO app_runtime_sessions(token_hash,user_id,app_id,scopes,expires_at) VALUES($1,$2,'reset-test','[]',$3)", [hashToken("reset-app-test"), account.user_id, new Date(start.getTime() + 86400_000)]);
  await admin.query("INSERT INTO auth_handoff_tokens(hashed_token,user_id,redirect_path,expires_at) VALUES($1,$2,'/settings',$3)", [hashToken("reset-handoff-test"), account.user_id, new Date(start.getTime() + 60000)]);
  const { link, token } = await f.issue(` ${account.email.toUpperCase()} `);
  const stored = (await admin.query("SELECT hashed_token,expires_at FROM password_reset_tokens WHERE user_id=$1", [account.user_id])).rows[0];
  expect(stored.hashed_token).toBe(hashToken(token));
  expect(stored.expires_at).toEqual(new Date(start.getTime() + 900000));
  const opened = await f.app.request(link);
  expect(opened.status).toBe(303);
  expect(opened.headers.get("Location")).toBe("https://apps.mistysys.com/#/reset");
  expect(opened.headers.get("Referrer-Policy")).toBe("no-referrer");
  for (const flag of ["HttpOnly", "Secure", "SameSite=Lax", "Max-Age=900", "Path=/"]) expect(opened.headers.get("Set-Cookie")).toContain(flag);
  const headers = { Cookie: `misty_reset_token=${token}` };
  expect((await f.app.request("/auth/reset/validate", { headers })).status).toBe(200);
  const reset = await f.post("/v1/auth/reset", { new_password: "new-test-password" }, headers);
  expect(reset.status).toBe(200);
  expect(reset.headers.get("Set-Cookie")).toContain("Max-Age=0");
  for (const table of ["sessions", "app_runtime_sessions", "auth_handoff_tokens", "password_reset_tokens"]) {
    expect((await admin.query(`SELECT count(*) FROM ${table} WHERE user_id=$1`, [account.user_id])).rows[0].count).toBe("0");
  }
  expect(await f.auth.authenticate(account.token)).toBeNull();
  expect(await f.auth.authenticate(other.token)).not.toBeNull();
  expect((await f.post("/login", { email: account.email, password: "old-test-password" })).status).toBe(401);
  expect((await f.post("/login", { email: account.email, password: "new-test-password" })).status).toBe(200);
  expect((await f.post("/auth/reset", { new_password: "replayed-password" }, headers)).status).toBe(400);
});

it("allows only one of two competing reset submissions to change the password", async () => {
  const f = fixture(), account = await f.register(), { token } = await f.issue(account.email);
  const results = await Promise.all(["first-new-password", "second-new-password"].map((new_password) => f.post("/auth/reset", { new_password }, { Cookie: `misty_reset_token=${token}` })));
  expect(results.map((result) => result.status).sort()).toEqual([200, 400]);
  const hash = (await admin.query("SELECT password_hash FROM users WHERE id=$1", [account.user_id])).rows[0].password_hash;
  expect(await passwords.verify(results[0]!.status === 200 ? "first-new-password" : "second-new-password", hash)).toBe(true);
});

it("does not consume a replacement token while processing an old, expired or invalid request", async () => {
  const f = fixture(), account = await f.register(), first = await f.issue(account.email), second = await f.issue(account.email);
  expect((await f.post("/auth/reset", { new_password: "stale-password" }, { Cookie: `misty_reset_token=${first.token}` })).status).toBe(400);
  expect(await f.recovery.validate(second.token)).toBe(true);
  expect((await f.post("/auth/reset", { new_password: "🔒".repeat(19) }, { Cookie: `misty_reset_token=${second.token}` })).status).toBe(400);
  expect(await f.recovery.validate(second.token)).toBe(true);
  expect((await f.post("/auth/reset", { new_password: "must-fail", token: second.token })).status).toBe(400);
  f.setTime(new Date(start.getTime() + 900000));
  expect(await f.recovery.validate(second.token)).toBe(false);
  expect((await f.app.request(`/auth/reset/validate?token=${second.token}`)).status).toBe(404);
  expect((await f.app.request(second.link)).headers.get("Set-Cookie")).toContain("Max-Age=0");
});

it("rolls back password/token changes if credential revocation fails", async () => {
  const f = fixture(), account = await f.register(), { token } = await f.issue(account.email);
  await admin.query("REVOKE DELETE ON auth_handoff_tokens FROM misty_hono_app_test");
  try {
    const response = await f.post("/auth/reset", { new_password: "must-rollback" }, { Cookie: `misty_reset_token=${token}` });
    expect(response.status).toBe(500);
    expect(await f.recovery.validate(token)).toBe(true);
    expect(await f.auth.authenticate(account.token)).not.toBeNull();
    const hash = (await admin.query("SELECT password_hash FROM users WHERE id=$1", [account.user_id])).rows[0].password_hash;
    expect(await passwords.verify("old-test-password", hash)).toBe(true);
  } finally { await admin.query("GRANT DELETE ON auth_handoff_tokens TO misty_hono_app_test"); }
  expect((await f.post("/auth/reset", { new_password: "retry-password" }, { Cookie: `misty_reset_token=${token}` })).status).toBe(200);
});

it("keeps unknown, deactivated and delivery-failure responses generic without logging reset links", async () => {
  const f = fixture(), account = await f.register();
  const unknown = await f.post("/auth/forgot", { email: "absent@example.invalid" });
  expect(unknown.status).toBe(202);
  expect(f.messages).toHaveLength(0);
  await f.jobs.runOnce();
  const issued = await f.issue(account.email);
  await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [account.user_id]);
  expect(await f.recovery.validate(issued.token)).toBe(false);
  const inactive = await f.post("/auth/forgot", { email: account.email });
  expect(await inactive.json()).toEqual(await unknown.json());
  expect(f.messages).toHaveLength(1);
  await f.jobs.runOnce();
  const failing = fixture(async () => { throw new Error("secret-reset-token@example.invalid"); });
  const active = await failing.register();
  expect((await failing.post("/auth/forgot", { email: active.email })).status).toBe(202);
  await failing.jobs.runOnce();
  expect(failing.logs.join("")).toContain("password recovery job failed");
  expect(failing.logs.join("")).not.toContain("secret-reset-token");
  expect(failing.logs.join("")).not.toContain(active.email);
});

it("shares recovery limits across aliases and rejects cross-site reset requests", async () => {
  const f = fixture();
  for (let index = 0; index < 5; index++) expect((await f.post(index % 2 ? "/auth/forgot" : "/v1/auth/forgot", { email: "limited@example.invalid" })).status).toBe(202);
  const limited = await f.post("/api/auth/forgot", { email: "limited@example.invalid" });
  expect(limited.status).toBe(429);
  expect(limited.headers.get("Retry-After")).toBe("900");
  expect((await f.post("/auth/reset", { new_password: "password" }, { Origin: "https://attacker.invalid" })).status).toBe(403);
});

it("retries the identical link after a worker restart and token-key rotation", async () => {
  const sent: EmailMessage[] = [];
  const f = fixture(async (message) => { sent.push(message); throw new Error("Lost provider acknowledgement"); });
  const account = await f.register();
  expect((await f.post("/auth/forgot", { email: account.email })).status).toBe(202);
  expect(sent).toHaveLength(0);
  await f.jobs.runOnce();
  expect(sent).toHaveLength(1);
  const stored = (await admin.query("SELECT hashed_token FROM password_reset_tokens WHERE user_id=$1", [account.user_id])).rows[0];
  f.setTime(new Date(start.getTime() + 3000));
  const restarted = f.makeJobs(async (message) => { sent.push(message); }, { active: "new", keys: new Map([["test", Buffer.alloc(32, 17)], ["new", Buffer.alloc(32, 18)]]) });
  expect(await restarted.runOnce()).toBe(true);
  expect(sent).toHaveLength(2);
  expect(sent[1]).toEqual(sent[0]);
  expect((await admin.query("SELECT hashed_token FROM password_reset_tokens WHERE user_id=$1", [account.user_id])).rows[0]).toEqual(stored);
  expect((await admin.query("SELECT state,attempts FROM password_recovery_jobs WHERE email=$1", [account.email])).rows[0]).toEqual({ state: "sent", attempts: 2 });
  expect(await restarted.runOnce()).toBe(false);
});

it("fences a late failed sender after another worker reclaims its expired lease", async () => {
  let release!: () => void, began!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; }), started = new Promise<void>((resolve) => { began = resolve; });
  let sends = 0;
  const f = fixture(async () => { sends++; if (sends === 1) { began(); await gate; throw new Error("Late failure"); } });
  const account = await f.register();
  await f.post("/auth/forgot", { email: account.email });
  const first = f.jobs.runOnce();
  await started;
  f.setTime(new Date(start.getTime() + 61000));
  await f.jobs.runOnce();
  release(); await first;
  expect(sends).toBe(2);
  expect((await admin.query("SELECT state,lease_owner FROM password_recovery_jobs WHERE email=$1", [account.email])).rows[0]).toEqual({ state: "sent", lease_owner: null });
});

it("does not resurrect consumed reset links from retrying or not-yet-issued jobs", async () => {
  const sent: EmailMessage[] = [];
  const f = fixture(async (message) => { sent.push(message); throw new Error("Ambiguous delivery"); }), account = await f.register();
  await f.post("/auth/forgot", { email: account.email }); await f.jobs.runOnce();
  const token = new URL(sent[0]!.text.split("\n").find((line) => line.startsWith("https://"))!).searchParams.get("token")!;
  await f.post("/auth/forgot", { email: account.email });
  expect((await f.post("/auth/reset", { new_password: "recovered-password" }, { Cookie: `misty_reset_token=${token}` })).status).toBe(200);
  f.setTime(new Date(start.getTime() + 3000));
  expect(await f.jobs.runOnce()).toBe(false);
  expect(sent).toHaveLength(1);
  expect((await admin.query("SELECT count(*) FROM password_reset_tokens WHERE user_id=$1", [account.user_id])).rows[0].count).toBe("0");
});

it("preserves unexpired credentials while cleaning expired rows and old delivery history", async () => {
  const f = fixture(), account = await f.register();
  await f.issue(account.email);
  for (const [name, expires] of [["old", start.getTime() - 25 * 3600000], ["recent", start.getTime() - 60000], ["future", start.getTime() + 2 * 86400_000]] as const) {
    await admin.query(`INSERT INTO connection_authorization_requests(state_hash,user_id,provider,actor,credential_snapshot,capabilities,requested_scopes,verifier_ciphertext,verifier_nonce,redirect_uri,client_id_hash,return_to,expires_at)
      VALUES($1,$2,'google','{}','[]','[]','[]','encrypted'::bytea,'nonce'::bytea,'https://api.example.invalid/callback',$1,$3,$4)`, [hashToken(`${account.user_id}-${name}`), account.user_id, name, new Date(expires)]);
    await admin.query(`INSERT INTO connected_account_oauth_states(state_hash,user_id,provider,capabilities,requested_scopes,verifier_ciphertext,verifier_nonce,return_to,expires_at)
      VALUES($1,$2,'google','[]','[]','encrypted'::bytea,'nonce'::bytea,$3,$4)`, [hashToken(`${account.user_id}-${name}`), account.user_id, name, new Date(expires)]);
  }
  const cleanup = createAuthCleanup(application, () => new Date(start.getTime() + 900000));
  await cleanup.runOnce();
  expect((await admin.query("SELECT return_to FROM connection_authorization_requests WHERE user_id=$1 ORDER BY return_to", [account.user_id])).rows.map((row) => row.return_to)).toEqual(["future", "recent"]);
  expect((await admin.query("SELECT return_to FROM connected_account_oauth_states WHERE user_id=$1", [account.user_id])).rows.map((row) => row.return_to)).toEqual(["future"]);
  expect((await admin.query("SELECT count(*) FROM password_reset_tokens WHERE user_id=$1", [account.user_id])).rows[0].count).toBe("0");
  expect(await f.auth.authenticate(account.token)).not.toBeNull();
  expect((await admin.query("SELECT count(*) FROM password_recovery_jobs WHERE email=$1", [account.email])).rows[0].count).toBe("1");
  await createAuthCleanup(application, () => new Date(start.getTime() + 86400_000 + 900000)).runOnce();
  expect((await admin.query("SELECT count(*) FROM password_recovery_jobs WHERE email=$1", [account.email])).rows[0].count).toBe("0");
});

it("only issues the newest pending request even when two requests share a timestamp", async () => {
  const f = fixture(), account = await f.register();
  await f.post("/auth/forgot", { email: account.email });
  await f.post("/auth/forgot", { email: account.email });
  await f.jobs.runOnce();
  await f.jobs.runOnce();
  expect(f.messages).toHaveLength(1);
  expect((await admin.query("SELECT state FROM password_recovery_jobs WHERE email=$1 ORDER BY request_order", [account.email])).rows.map((row) => row.state)).toEqual(["superseded", "sent"]);
});

it("accepts an unexpired reset token created by the existing Go schema", async () => {
  const f = fixture(), account = await f.register(), token = randomUUID().replaceAll("-", "");
  await admin.query("INSERT INTO password_reset_tokens(user_id,hashed_token,expires_at) VALUES($1,$2,$3)", [account.user_id, hashToken(token), new Date(start.getTime() + 900000)]);
  expect(await f.recovery.validate(token)).toBe(true);
  expect((await f.post("/auth/reset", { new_password: "legacy-reset-password" }, { Cookie: `misty_reset_token=${token}` })).status).toBe(200);
  expect((await f.post("/login", { email: account.email, password: "legacy-reset-password" })).status).toBe(200);
});

it("does not acknowledge recovery requests when durable enqueue fails", async () => {
  const f = fixture(), account = await f.register();
  await admin.query("REVOKE INSERT ON password_recovery_jobs FROM misty_hono_app_test");
  try {
    for (const email of [account.email, "absent@example.invalid"]) {
      const response = await f.post("/auth/forgot", { email });
      expect(response.status).toBe(503);
      expect(response.headers.get("Retry-After")).toBe("1");
    }
    expect(f.messages).toHaveLength(0);
  } finally { await admin.query("GRANT INSERT ON password_recovery_jobs TO misty_hono_app_test"); }
});
