import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { generateKeyPair, SignJWT } from "jose";
import { pino } from "pino";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { createRequestBoundary } from "../../../../../packages/runtime/src/request-boundary.js";
import { createApi } from "../../app.js";
import { createAuthRepository } from "./repository.js";
import { createPasswordHasher, type PasswordHasher } from "./passwords.js";
import { createAuthService, hashToken } from "./service.js";
import { sessionToken } from "./routes.js";
import { createSelfHostProofVerifier } from "../self-host/proof.js";
import { createHandoffRepository } from "./handoff/repository.js";
import { createHandoffService } from "./handoff/service.js";

const admin = createTestDatabase();
let application: Pool, passwords: PasswordHasher;
const emails: string[] = [];
const start = new Date("2026-09-05T12:00:00Z");
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_hono_app_test') THEN
    CREATE ROLE misty_hono_app_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE ON users,licenses TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON sessions,auth_handoff_tokens TO misty_hono_app_test;
    GRANT SELECT,UPDATE ON self_host_accounts TO misty_hono_app_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_app_test", max: 4 });
  passwords = await createPasswordHasher();
}, 60000);
afterEach(async () => { await admin.query("DELETE FROM users WHERE email=ANY($1::text[])", [emails]); emails.length = 0; });
afterAll(async () => { if (application) await application.end(); await admin.end(); });

function identity() {
  const username = `auth_${randomUUID().replaceAll("-", "").slice(0, 12)}`, email = `${username}@example.invalid`;
  emails.push(email);
  return { name: "Auth test", username, email, password: "migration-test-password" };
}
function fixture(options: { deployment?: "hosted" | "self_hosted"; passwords?: PasswordHasher;
  verifySelfHostProof?: (token: string | undefined) => Promise<{ subject: string; expiresAt: Date }> } = {}) {
  let now = start;
  const repository = createAuthRepository(application);
  const service = createAuthService({ repository, passwords: options.passwords ?? passwords, deployment: options.deployment ?? "hosted", now: () => now,
    ...(options.verifySelfHostProof ? { verifySelfHostProof: options.verifySelfHostProof } : {}) });
  const boundary = createRequestBoundary({ trustProxyHeaders: true, peerAddress: () => "127.0.0.1" });
  const handoffRepository = createHandoffRepository(application);
  const handoff = createHandoffService({ repository: handoffRepository, now: () => now,
    config: { startUrl: "https://api.example.invalid/v1/auth/handoff/start", websiteUrl: "https://apps.mistysys.com" } });
  const app = createApi({ logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    auth: { service, boundary, handoff, deployment: options.deployment ?? "hosted", now: () => now.getTime() } });
  app.get("/test-session", async (c) => {
    const token = sessionToken(c), user = token ? await service.authenticate(token) : null;
    return user ? c.json({ id: user.id }) : c.json({ code: "not_authenticated" }, 401);
  });
  // Ensure auth body limits do not leak to unrelated domain routers.
  app.post("/test-large-body", async (c) => c.json({ bytes: (await c.req.arrayBuffer()).byteLength }));
  const post = (path: string, body?: unknown, headers: Record<string, string> = {}) => app.request(path, {
    method: "POST", ...(body !== undefined ? { body: JSON.stringify(body) } : {}), headers: { "Content-Type": "application/json", ...headers },
  });
  return { app, post, service, repository, handoffRepository, setTime: (time: Date) => { now = time; } };
}

it("registers an account/license/session atomically and preserves hosted cookie and response contracts", async () => {
  const auth = fixture(), input = identity();
  const response = await auth.post("https://api.example.invalid/v1/register", { ...input, username: ` ${input.username.toUpperCase()} `, email: ` ${input.email.toUpperCase()} ` },
    { Origin: "https://apps.mistysys.com", "X-Misty-Analytics-Enabled": "true" });
  expect(response.status).toBe(201);
  const body = await response.json();
  expect(body).toMatchObject({ name: input.name, username: input.username, email: input.email });
  expect(body.token).toMatch(/^[A-Za-z0-9_-]{43}$/);
  const cookie = response.headers.get("Set-Cookie")!;
  for (const part of ["misty_session=", "HttpOnly", "Secure", "SameSite=None", "Max-Age=2592000", "Path=/"]) expect(cookie).toContain(part);
  expect(cookie).not.toContain("Domain=");
  expect(response.headers.get("Cache-Control")).toBe("no-store");
  const stored = (await admin.query("SELECT token_hash,expires_at FROM sessions WHERE user_id=$1", [body.user_id])).rows[0];
  expect(stored.token_hash).toBe(hashToken(body.token));
  expect(stored.expires_at).toEqual(new Date(start.getTime() + 30 * 86400_000));
  expect((await admin.query("SELECT l.tier,u.analytics_enabled FROM users u JOIN licenses l ON l.id=u.license_id WHERE u.id=$1", [body.user_id])).rows[0]).toEqual({ tier: "basic", analytics_enabled: true });
  expect(await auth.service.authenticate(body.token)).toMatchObject({ id: body.user_id });
});

it("logs in through legacy aliases, rejects bad credentials and revokes only the selected session", async () => {
  const auth = fixture(), input = identity();
  const created = await (await auth.post("/register", input)).json();
  expect((await auth.post("/api/login", { email: input.email, password: "wrong" })).status).toBe(401);
  const loggedIn = await auth.post("/api/login", { email: input.email, password: input.password });
  expect(loggedIn.status).toBe(200);
  expect(loggedIn.headers.get("Set-Cookie")).toContain("SameSite=Lax");
  const second = await loggedIn.json();
  expect((await auth.app.request("/test-session", { headers: { Cookie: `misty_session=${created.token}` } })).status).toBe(200);
  const loggedOut = await auth.post("/v1/logout", undefined, { Authorization: `Bearer ${second.token}`, Cookie: `misty_session=${created.token}` });
  expect(loggedOut.status).toBe(200);
  expect(loggedOut.headers.get("Set-Cookie")).toContain("Max-Age=0");
  expect(await auth.service.authenticate(second.token)).toBeNull();
  expect(await auth.service.authenticate(created.token)).not.toBeNull();
  auth.setTime(new Date(start.getTime() + 30 * 86400_000));
  expect(await auth.service.authenticate(created.token)).toBeNull();
});

it("does not accept downloaded-app tokens as account sessions or revive deactivated users", async () => {
  const auth = fixture(), input = identity();
  const created = await (await auth.post("/register", input)).json();
  await admin.query("INSERT INTO user_app_installations(user_id,app_id,installed_version,granted_scopes) VALUES($1,'test','1.0.0','[]')", [created.user_id]);
  await admin.query("INSERT INTO app_runtime_sessions(token_hash,user_id,app_id,scopes,expires_at) VALUES($1,$2,'test','[]',$3)", [hashToken("app-token"), created.user_id, new Date(start.getTime() + 86400_000)]);
  expect(await auth.service.authenticate("app-token")).toBeNull();
  await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [created.user_id]);
  expect(await auth.service.authenticate(created.token)).toBeNull();
  expect((await auth.post("/login", { email: input.email, password: input.password })).status).toBe(401);
});

it("handles duplicate registration races without orphan licenses or multiple sessions", async () => {
  const auth = fixture(), input = identity();
  const results = await Promise.all([auth.post("/register", input), auth.post("/api/register", input)]);
  expect(results.map((response) => response.status).sort()).toEqual([201, 409]);
  const stored = await admin.query("SELECT u.id,count(s.token_hash)::int sessions FROM users u JOIN sessions s ON s.user_id=u.id WHERE u.email=$1 GROUP BY u.id", [input.email]);
  expect(stored.rows).toHaveLength(1);
  expect(stored.rows[0].sessions).toBe(1);
});

it("rejects registration that collides with a legacy mixed-case account email", async () => {
  const auth = fixture(), input = identity();
  const created = await (await auth.post("/register", input)).json();
  const legacyEmail = input.email.toUpperCase();
  emails.push(legacyEmail);
  await admin.query("UPDATE users SET email=$2 WHERE id=$1", [created.user_id, legacyEmail]);
  const collision = await auth.post("/register", { ...input, username: `${input.username}_2` });
  expect(collision.status).toBe(409);
  expect((await admin.query("SELECT count(*) FROM users WHERE LOWER(email)=$1", [input.email])).rows[0].count).toBe("1");
  expect((await auth.post("/login", { email: input.email, password: input.password })).status).toBe(200);
});

it("rejects a password verified before a concurrent reset and does not create a replacement session", async () => {
  const initial = fixture(), input = identity();
  const created = await (await initial.post("/register", input)).json();
  const replacementHash = await passwords.hash("new-test-password");
  const racing = fixture({ passwords: { ...passwords, verify: async (password, hash) => {
    const valid = await passwords.verify(password, hash);
    await withTransaction(admin, async (tx) => {
      await tx.query("UPDATE users SET password_hash=$2 WHERE id=$1", [created.user_id, replacementHash]);
      await tx.query("DELETE FROM sessions WHERE user_id=$1", [created.user_id]);
    });
    return valid;
  } } });
  expect((await racing.post("/login", { email: input.email, password: input.password })).status).toBe(401);
  expect((await admin.query("SELECT count(*) FROM sessions WHERE user_id=$1", [created.user_id])).rows[0].count).toBe("0");
});

it("rejects malformed bodies and cross-site authentication while sharing rate limits across aliases", async () => {
  const auth = fixture(), input = identity();
  expect((await auth.post("/register", { ...input, role: "admin" })).status).toBe(400);
  expect((await auth.post("/register", { ...input, password: "🔒".repeat(19) })).status).toBe(400);
  expect((await auth.post("/register", input, { Origin: "https://attacker.invalid" })).status).toBe(403);
  expect((await auth.post("/test-large-body", { data: "x".repeat(9000) })).status).toBe(200);
  for (let index = 0; index < 20; index++) expect((await auth.post(index % 2 ? "/login" : "/v1/login", { email: "", password: "" })).status).toBe(400);
  const limited = await auth.post("/api/login", { email: input.email, password: input.password });
  expect(limited.status).toBe(429);
  expect(limited.headers.get("Retry-After")).toBe("60");
});

it("requires a valid subject-bound self-host proof and keeps public registration closed", async () => {
  const hosted = fixture(), input = identity();
  const created = await (await hosted.post("/register", input)).json();
  const subject = `hosted-subject-${randomUUID()}`;
  await admin.query("INSERT INTO self_host_accounts(user_id,entitlement_subject,entitlement_expires_at) VALUES($1,$2,$3)", [created.user_id, subject, start]);
  const keys = await generateKeyPair("EdDSA", { crv: "Ed25519" });
  const auth = fixture({ deployment: "self_hosted", verifySelfHostProof: createSelfHostProofVerifier(new Map([["test-key", keys.publicKey]]), () => start) });
  const body = { email: input.email, password: input.password };
  expect((await auth.post("/register", input)).status).toBe(403);
  expect((await auth.post("/login", body)).status).toBe(402);
  const sign = (sub: string, lifetime: number) => new SignJWT({ sub, status: "eligible", schema_version: 1 })
    .setProtectedHeader({ alg: "EdDSA", kid: "test-key", typ: "JWT" }).setIssuer("misty-hosted").setAudience("misty-self-hosted")
    .setJti(randomUUID()).setIssuedAt(Math.floor(start.getTime() / 1000)).setExpirationTime(Math.floor(start.getTime() / 1000) + lifetime).sign(keys.privateKey);
  expect((await auth.post("/login", body, { "X-Misty-Self-Hosted-Entitlement": await sign("another-hosted-subject", 86400) })).status).toBe(403);
  expect((await auth.post("/login", body, { "X-Misty-Self-Hosted-Entitlement": await sign(subject, 8 * 86400) })).status).toBe(402);
  expect((await auth.post("/login", body, { "X-Misty-Self-Hosted-Entitlement": await sign(subject, 86400) })).status).toBe(200);
  expect((await admin.query("SELECT entitlement_expires_at FROM self_host_accounts WHERE user_id=$1", [created.user_id])).rows[0].entitlement_expires_at).toEqual(new Date(start.getTime() + 86400_000));
});

it("redeems a browser handoff exactly once and keeps the one-time credential out of the destination", async () => {
  const auth = fixture(), input = identity();
  const created = await (await auth.post("/register", input)).json();
  const minted = await auth.post("/api/auth/handoff", { path: "/settings/billing" }, { Authorization: `Bearer ${created.token}` });
  expect(minted.status).toBe(200);
  const { url } = await minted.json();
  const stored = (await admin.query("SELECT hashed_token,expires_at FROM auth_handoff_tokens WHERE user_id=$1", [created.user_id])).rows[0];
  expect(stored.hashed_token).toBe(hashToken(new URL(url).searchParams.get("token")!));
  expect(stored.expires_at).toEqual(new Date(start.getTime() + 60000));
  const results = await Promise.all([auth.app.request(url), auth.app.request(url)]);
  expect(results.map((result) => result.status)).toEqual([303, 303]);
  const accepted = results.find((result) => result.headers.has("Set-Cookie"))!;
  expect(accepted).toBeDefined();
  expect(results.filter((result) => result.headers.has("Set-Cookie"))).toHaveLength(1);
  expect(accepted.headers.get("Location")).toBe("https://apps.mistysys.com/settings/billing");
  expect(accepted.headers.get("Cache-Control")).toBe("no-store");
  expect(accepted.headers.get("Referrer-Policy")).toBe("no-referrer");
  expect(accepted.headers.get("Set-Cookie")).toContain("Max-Age=43200");
  const browserToken = /misty_session=([^;]+)/.exec(accepted.headers.get("Set-Cookie")!)![1]!;
  expect(await auth.service.authenticate(browserToken)).toMatchObject({ id: created.user_id });
  expect((await admin.query("SELECT count(*) FROM sessions WHERE user_id=$1", [created.user_id])).rows[0].count).toBe("2");
  auth.setTime(new Date(start.getTime() + 43200_000));
  expect(await auth.service.authenticate(browserToken)).toBeNull();
});

it("burns expired and deactivated-account handoff links without issuing a browser session", async () => {
  const auth = fixture(), input = identity();
  const created = await (await auth.post("/register", input)).json();
  const headers = { Authorization: `Bearer ${created.token}` };
  const expired = await (await auth.post("/auth/handoff", undefined, headers)).json();
  auth.setTime(new Date(start.getTime() + 60000));
  const response = await auth.app.request(expired.url);
  expect(response.status).toBe(303);
  expect(response.headers.has("Set-Cookie")).toBe(false);
  expect((await admin.query("SELECT count(*) FROM auth_handoff_tokens WHERE user_id=$1", [created.user_id])).rows[0].count).toBe("0");
  const deactivated = await (await auth.post("/auth/handoff", undefined, headers)).json();
  await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [created.user_id]);
  expect((await auth.app.request(deactivated.url)).headers.has("Set-Cookie")).toBe(false);
  expect((await auth.post("/auth/handoff", undefined, headers)).status).toBe(401);
});

it("rolls handoff consumption back if replacement session creation fails", async () => {
  const auth = fixture(), input = identity();
  const created = await (await auth.post("/register", input)).json();
  const minted = await (await auth.post("/auth/handoff", undefined, { Authorization: `Bearer ${created.token}` })).json();
  const tokenHash = hashToken(new URL(minted.url).searchParams.get("token")!);
  await expect(auth.handoffRepository.redeem({ tokenHash, sessionHash: hashToken(created.token), now: start,
    sessionExpiresAt: new Date(start.getTime() + 43200_000) })).rejects.toMatchObject({ code: "23505" });
  expect((await admin.query("SELECT count(*) FROM auth_handoff_tokens WHERE hashed_token=$1", [tokenHash])).rows[0].count).toBe("1");
  expect((await auth.app.request(minted.url)).headers.has("Set-Cookie")).toBe(true);
});

it("rejects untrusted handoff destinations and requires a live full account session", async () => {
  const auth = fixture(), input = identity();
  const created = await (await auth.post("/register", input)).json();
  const headers = { Authorization: `Bearer ${created.token}` };
  expect((await auth.post("/auth/handoff", { path: "//attacker.invalid" }, headers)).status).toBe(400);
  expect((await auth.post("/auth/handoff", undefined)).status).toBe(401);
  expect((await auth.post("/auth/handoff", undefined, { Authorization: "Bearer not-an-account-session" })).status).toBe(401);
  const minted = await (await auth.post("/auth/handoff", undefined, headers)).json();
  await admin.query("UPDATE auth_handoff_tokens SET redirect_path='//attacker.invalid' WHERE user_id=$1", [created.user_id]);
  expect((await auth.app.request(minted.url)).headers.get("Location")).toBe("https://apps.mistysys.com/settings");
  await auth.service.logout(created.token);
  expect((await auth.post("/auth/handoff", undefined, headers)).status).toBe(401);
});
