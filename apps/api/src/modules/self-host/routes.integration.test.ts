import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { generateKeyPair, SignJWT } from "jose";
import { Pool } from "pg";
import { pino } from "pino";
import { afterAll, afterEach, beforeAll, beforeEach, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { createRequestBoundary } from "../../../../../packages/runtime/src/request-boundary.js";
import { createApi } from "../../app.js";
import { createAuthRepository } from "../auth/repository.js";
import { createAuthService, hashToken } from "../auth/service.js";
import { createPasswordHasher, type PasswordHasher } from "../auth/passwords.js";
import { createAppRuntimeRepository } from "../app-runtime/repository.js";
import { loadInstanceConfig } from "./config.js";
import { createSelfHostRepository } from "./repository.js";
import { createSelfHostService } from "./service.js";
import { createSelfHostProofVerifier } from "./proof.js";
import { createSelfHostAdmin } from "./admin.js";
import { execFile } from "node:child_process";

const admin = createTestDatabase(), emails: string[] = [], bootstrapHashes: string[] = [];
let application: Pool, passwords: PasswordHasher, keys: Awaited<ReturnType<typeof generateKeyPair>>;
const start = new Date("2026-09-05T12:00:00Z");
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_hono_app_test') THEN CREATE ROLE misty_hono_app_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON users,licenses,sessions,misty_instance,self_host_accounts,self_host_bootstrap_tokens,self_host_enrollment_invitations,app_runtime_sessions,user_app_installations,password_reset_tokens,auth_handoff_tokens,password_recovery_jobs TO misty_hono_app_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_app_test", max: 4 });
  passwords = await createPasswordHasher(); keys = await generateKeyPair("EdDSA", { crv: "Ed25519" });
}, 60000);
beforeEach(async () => { expect((await admin.query("SELECT count(*) FROM self_host_accounts")).rows[0].count).toBe("0"); });
afterEach(async () => {
  await admin.query("DELETE FROM users WHERE email=ANY($1::text[])", [emails]); emails.length = 0;
  await admin.query("DELETE FROM self_host_bootstrap_tokens WHERE token_hash=ANY($1::text[])", [bootstrapHashes]); bootstrapHashes.length = 0;
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });

function fixture(deployment: "hosted" | "self_hosted" = "self_hosted") {
  let now = start;
  const verify = createSelfHostProofVerifier(new Map([["self-host-test", keys.publicKey]]), () => now);
  const auth = createAuthService({ repository: createAuthRepository(application), passwords, deployment, verifySelfHostProof: verify, now: () => now });
  const repository = createSelfHostRepository(application, () => now);
  const management = createSelfHostAdmin(application, passwords, () => now);
  const service = createSelfHostService({ repository, passwords, verify, config: loadInstanceConfig({ MISTY_INSTANCE_NAME: "Test instance", MISTY_LIBRARY_FILESYSTEM_DIR: "/tmp/test-library" }, deployment), now: () => now });
  const boundary = createRequestBoundary({ peerAddress: () => "127.0.0.1" });
  const app = createApi({ logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    auth: { service: auth, boundary, deployment, now: () => now.getTime() }, selfHost: { service, auth, boundary, deployment, now: () => now.getTime() },
    appRuntime: { repository: createAppRuntimeRepository(application) } });
  app.get("/test-resource", (c) => c.json({ ok: true }));
  const post = (path: string, body: unknown = {}, headers: Record<string, string> = {}) => app.request(path, { method: "POST", body: JSON.stringify(body), headers: { "Content-Type": "application/json", ...headers } });
  const input = () => {
    const username = `self_${randomUUID().replaceAll("-", "").slice(0, 12)}`, email = `${username}@example.invalid`;
    emails.push(email); return { name: "Self host test", username, email, password: "self-host-test-password" };
  };
  const proof = async (subject = `hosted-subject-${randomUUID()}`, lifetime = 86400) => {
    const seconds = Math.floor(now.getTime() / 1000);
    const token = await new SignJWT({ sub: subject, status: "eligible", schema_version: 1 }).setProtectedHeader({ alg: "EdDSA", kid: "self-host-test" })
      .setIssuer("misty-hosted").setAudience("misty-self-hosted").setIssuedAt(seconds).setExpirationTime(seconds + lifetime).setJti(randomUUID()).sign(keys.privateKey);
    return { subject, token };
  };
  const bootstrapToken = async () => {
    const { token } = await management.bootstrapToken(); bootstrapHashes.push(hashToken(token));
    return token;
  };
  const bootstrap = async () => {
    const body = input(), verified = await proof(), token = await bootstrapToken();
    const response = await post("/v1/self-host/bootstrap", { ...body, bootstrap_token: token }, { "X-Misty-Self-Hosted-Entitlement": verified.token });
    expect(response.status, await response.clone().text()).toBe(201);
    return { ...await response.json(), subject: verified.subject, email: body.email } as { user_id: string; token: string; subject: string; email: string };
  };
  return { app, auth, service, repository, management, post, input, proof, bootstrapToken, bootstrap, setTime: (time: Date) => { now = time; } };
}

it("preserves durable instance identity and changes bootstrap status after the first account", async () => {
  const f = fixture();
  const initial = await (await f.app.request("/api/instance")).json();
  expect(initial).toMatchObject({ name: "Test instance", deployment: "self_hosted", protocol_version: 1, registration: "invitation", bootstrap_required: true,
    capabilities: { hosted_ai: false, hosted_billing: false, storage_backend: "filesystem" } });
  expect(initial.server_id).toMatch(/^server_[0-9a-f-]{36}$/);
  await f.bootstrap();
  const after = await (await f.app.request("/v1/instance")).json();
  expect(after.server_id).toBe(initial.server_id); expect(after.bootstrap_required).toBe(false);
});

it("serializes first-admin creation even when competing requests use different bootstrap tokens", async () => {
  const f = fixture(), first = f.input(), second = f.input();
  const proofs = await Promise.all([f.proof(), f.proof()]), tokens = await Promise.all([f.bootstrapToken(), f.bootstrapToken()]);
  const responses = await Promise.all([first, second].map((body, index) => f.post("/self-host/bootstrap", { ...body, bootstrap_token: tokens[index] }, { "X-Misty-Self-Hosted-Entitlement": proofs[index]!.token })));
  expect(responses.map((response) => response.status).sort()).toEqual([201, 410]);
  expect((await admin.query("SELECT count(*) FROM self_host_accounts WHERE is_admin")).rows[0].count).toBe("1");
  expect((await admin.query("SELECT count(*) FROM self_host_bootstrap_tokens WHERE token_hash=ANY($1::text[]) AND consumed_at IS NOT NULL", [bootstrapHashes])).rows[0].count).toBe("1");
});

it("rolls account creation and bootstrap consumption back if session creation fails", async () => {
  const f = fixture(), body = f.input(), token = await f.bootstrapToken(), proof = await f.proof();
  await admin.query("REVOKE INSERT ON sessions FROM misty_hono_app_test");
  try {
    expect((await f.post("/self-host/bootstrap", { ...body, bootstrap_token: token }, { "X-Misty-Self-Hosted-Entitlement": proof.token })).status).toBe(500);
    expect((await admin.query("SELECT count(*) FROM users WHERE email=$1", [body.email])).rows[0].count).toBe("0");
    expect((await admin.query("SELECT consumed_at FROM self_host_bootstrap_tokens WHERE token_hash=$1", [hashToken(token)])).rows[0].consumed_at).toBeNull();
  } finally { await admin.query("GRANT INSERT ON sessions TO misty_hono_app_test"); }
  expect((await f.post("/self-host/bootstrap", { ...body, bootstrap_token: token }, { "X-Misty-Self-Hosted-Entitlement": proof.token })).status).toBe(201);
});

it("enrolls an invited account once, rejects duplicate subjects and requires admin invitations", async () => {
  const f = fixture(), owner = await f.bootstrap(), headers = { Authorization: `Bearer ${owner.token}` };
  const invitation = await (await f.post("/api/self-host/invitations", {}, headers)).json();
  const duplicate = await f.proof(owner.subject);
  expect((await f.post("/self-host/enroll", { ...f.input(), invitation: invitation.invitation }, { "X-Misty-Self-Hosted-Entitlement": duplicate.token })).status).toBe(409);
  const proof = await f.proof(), body = f.input();
  const enrolled = await f.post("/self-host/enroll", { ...body, invitation: invitation.invitation }, { "X-Misty-Self-Hosted-Entitlement": proof.token });
  expect(enrolled.status).toBe(201);
  const member = await enrolled.json();
  expect((await f.post("/self-host/invitations", {}, { Authorization: `Bearer ${member.token}` })).status).toBe(403);
  expect((await f.post("/self-host/enroll", { ...f.input(), invitation: invitation.invitation }, { "X-Misty-Self-Hosted-Entitlement": (await f.proof()).token })).status).toBe(410);
  const revocable = await (await f.post("/self-host/invitations", {}, headers)).json();
  expect((await f.app.request(`/v1/self-host/invitations/${revocable.id}`, { method: "DELETE", headers })).status).toBe(204);
  expect((await f.post("/self-host/enroll", { ...f.input(), invitation: revocable.invitation }, { "X-Misty-Self-Hosted-Entitlement": (await f.proof()).token })).status).toBe(410);
});

it("requires current cached access while allowing subject-bound renewal and logout", async () => {
  const f = fixture(), owner = await f.bootstrap(), headers = { Authorization: `Bearer ${owner.token}` };
  expect((await f.app.request("/test-resource", { headers })).status).toBe(200);
  f.setTime(new Date(start.getTime() + 86400_000));
  expect((await f.app.request("/test-resource", { headers })).status).toBe(402);
  expect((await f.post("/self-host/invitations", {}, headers)).status).toBe(402);
  expect((await f.post("/self-host/entitlement", {}, { ...headers, "X-Misty-Self-Hosted-Entitlement": (await f.proof()).token })).status).toBe(403);
  expect((await f.post("/self-host/entitlement", {}, { ...headers, "X-Misty-Self-Hosted-Entitlement": (await f.proof(owner.subject)).token })).status).toBe(200);
  expect((await f.app.request("/test-resource", { headers })).status).toBe(200);
  await admin.query("UPDATE self_host_accounts SET disabled_at=$2 WHERE user_id=$1", [owner.user_id, start]);
  expect((await f.app.request("/test-resource", { headers })).status).toBe(402);
  expect((await f.post("/self-host/entitlement", {}, { ...headers, "X-Misty-Self-Hosted-Entitlement": (await f.proof(owner.subject)).token })).status).toBe(403);
  expect((await f.post("/logout", {}, headers)).status).toBe(200);
});

it("applies the cached entitlement gate to downloaded-app sessions without granting account powers", async () => {
  const f = fixture(), owner = await f.bootstrap(), token = `self-host-app-${randomUUID()}`;
  await admin.query("INSERT INTO user_app_installations(user_id,app_id,installed_version,granted_scopes) VALUES($1,'self-test','1.0.0','[]')", [owner.user_id]);
  await admin.query("INSERT INTO app_runtime_sessions(token_hash,user_id,app_id,scopes,expires_at) VALUES($1,$2,'self-test','[]','2099-01-01')", [hashToken(token), owner.user_id]);
  const headers = { Authorization: `Bearer ${token}` };
  expect((await f.post("/self-host/invitations", {}, headers)).status).toBe(401);
  await admin.query("UPDATE self_host_accounts SET entitlement_expires_at=$2 WHERE user_id=$1", [owner.user_id, start]);
  expect((await f.app.request("/app-runtime/session", { headers })).status).toBe(402);
});

it("keeps hosted-only features closed and self-host setup unavailable on hosted deployments", async () => {
  const f = fixture();
  for (const path of ["/auth/forgot", "/api/auth/reset", "/v1/auth/handoff", "/billing/checkout", "/spaces/space/agents/run", "/ai/chat"]) {
    expect((await f.post(path)).status).toBe(501);
  }
  expect((await f.post("/register", f.input())).status).toBe(403);
  const hosted = fixture("hosted");
  expect((await hosted.post("/self-host/bootstrap", hosted.input())).status).toBe(404);
  const descriptor = await (await hosted.app.request("/instance")).json();
  expect(descriptor).toMatchObject({ deployment: "hosted", bootstrap_required: false, registration: "open", capabilities: { hosted_ai: true } });
});

it("native admin resets passwords and disables accounts while revoking outstanding credentials", async () => {
  const f = fixture(), owner = await f.bootstrap();
  await expect(f.management.bootstrapToken()).rejects.toMatchObject({ code: "bootstrap_token_invalid" });
  await f.management.changeAccount(owner.email, { kind: "password", password: "administrator-reset-password" });
  expect(await f.auth.authenticate(owner.token)).toBeNull();
  const login = await f.post("/login", { email: owner.email, password: "administrator-reset-password" }, { "X-Misty-Self-Hosted-Entitlement": (await f.proof(owner.subject)).token });
  expect(login.status).toBe(200);
  const session = await login.json();
  const invitation = await (await f.post("/self-host/invitations", {}, { Authorization: `Bearer ${session.token}` })).json();
  await f.management.changeAccount(owner.email, { kind: "disable" });
  expect(await f.auth.authenticate(session.token)).toBeNull();
  expect((await f.post("/login", { email: owner.email, password: "administrator-reset-password" }, { "X-Misty-Self-Hosted-Entitlement": (await f.proof(owner.subject)).token })).status).toBe(403);
  expect((await f.post("/self-host/enroll", { ...f.input(), invitation: invitation.invitation }, { "X-Misty-Self-Hosted-Entitlement": (await f.proof()).token })).status).toBe(410);
});

it("runs the native administrative CLI with a password from stdin and keeps it out of output", async () => {
  const f = fixture(), owner = await f.bootstrap(), database = new URL(process.env.MISTY_TEST_DATABASE_URL!);
  const run = (args: string[], input = "", deployment = "self_hosted") => new Promise<{ stdout: string; stderr: string; code: number }>((resolve) => {
    const child = execFile(process.execPath, ["--import", "tsx", fileURLToPath(new URL("../../admin.ts", import.meta.url)), ...args], {
      env: { ...process.env, MISTY_DEPLOYMENT_MODE: deployment, DB_HOST: database.hostname, DB_PORT: database.port,
        DB_NAME: database.pathname.slice(1), DB_USER: decodeURIComponent(database.username), DB_PASSWORD: decodeURIComponent(database.password), DB_SSLMODE: "disable" },
      timeout: 10000, maxBuffer: 4096,
    }, (error, stdout, stderr) => resolve({ stdout, stderr, code: error ? Number(error.code ?? 1) : 0 }));
    child.stdin!.end(input);
  });
  const reset = await run(["reset-password", "--email", owner.email], "stdin-test-password\n");
  expect(reset.code, reset.stderr).toBe(0);
  expect(reset.stdout).toContain("Password reset");
  expect(reset.stdout + reset.stderr).not.toContain("stdin-test-password");
  expect(await f.auth.authenticate(owner.token)).toBeNull();
  expect((await run(["reset-password", "--email", owner.email])).code).toBe(1);
  const disabled = await run(["disable-account", "--email", owner.email]);
  expect(disabled.code, disabled.stderr).toBe(0);
  expect((await run(["bootstrap-token"], "", "hosted")).code).toBe(1);
});
