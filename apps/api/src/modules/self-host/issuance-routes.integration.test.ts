import { generateKeyPairSync, randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { decodeJwt } from "jose";
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
import { createSelfHostEligibility } from "./eligibility.js";
import { createSelfHostIssuer } from "./issuer.js";

const admin = createTestDatabase(), userIds: string[] = [];
let revision = 0;
let application: Pool, passwords: PasswordHasher;
const now = new Date("2026-09-05T12:00:00Z");
const issuer = createSelfHostIssuer({ privateKey: generateKeyPairSync("ed25519").privateKey, keyId: "issuance-test", subjectSecret: Buffer.alloc(32, 71) });
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_hono_app_test') THEN CREATE ROLE misty_hono_app_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA public TO misty_hono_app_test;
    GRANT SELECT,INSERT,UPDATE ON users,licenses,sessions TO misty_hono_app_test;
    GRANT SELECT ON payment_entitlement_projections TO misty_hono_app_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_app_test", max: 4 });
  passwords = await createPasswordHasher();
}, 60000);
afterEach(async () => {
  await admin.query("DELETE FROM users WHERE id=ANY($1::text[])", [userIds]);
  await admin.query("DELETE FROM payment_entitlement_inbox WHERE user_id=ANY($1::text[])", [userIds]); userIds.length = 0;
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });

function fixture(sign: typeof issuer | null = issuer, deployment: "hosted" | "self_hosted" = "hosted") {
  const auth = createAuthService({ repository: createAuthRepository(application), passwords, deployment: "hosted", now: () => now });
  const boundary = createRequestBoundary({ peerAddress: () => "127.0.0.1" });
  const eligibility = createSelfHostEligibility(application);
  const app = createApi({ logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    auth: { service: auth, boundary, deployment, now: () => now.getTime() },
    selfHostIssuance: { auth, boundary, eligibility, issuer: sign, now: () => now } });
  const account = async () => {
    const username = `proof_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
    const result = await auth.register({ username, email: `${username}@example.invalid`, name: "Proof test", password: "test-password", analyticsEnabled: false });
    userIds.push(result.user.id); return result;
  };
  const post = (token: string, prefix = "/v1", headers: Record<string, string> = {}) => app.request(`${prefix}/billing/self-host-entitlement`, {
    method: "POST", headers: { Authorization: `Bearer ${token}`, ...headers },
  });
  return { auth, eligibility, app, account, post };
}
async function trial(userId: string, end: Date) {
  await admin.query("UPDATE licenses SET tier='pro',status='trialing',expires_at=$2 WHERE user_id=$1", [userId, end]);
}
async function projection(user: { id: string; license_id: string }, status: string, end: Date) {
  const eventId = randomUUID(), nextRevision = String(++revision);
  await admin.query("INSERT INTO payment_entitlement_inbox(event_id,user_id,revision,payload_sha256) VALUES($1,$2,$3,$4)", [eventId, user.id, nextRevision, "0".repeat(64)]);
  const payload = { version: 1, eventId, userId: user.id, licenseId: user.license_id, revision: nextRevision, generatedAt: now.toISOString(),
    subscription: { subscriptionId: "test-subscription", tier: "pro", interval: "month", status, currentPeriodEnd: end.toISOString(), cancelAtPeriodEnd: false } };
  await admin.query(`INSERT INTO payment_entitlement_projections(user_id,license_id,revision,event_id,payload) VALUES($1,$2,$5,$3,$4)
    ON CONFLICT(user_id) DO UPDATE SET revision=EXCLUDED.revision,event_id=EXCLUDED.event_id,payload=EXCLUDED.payload`, [user.id, user.license_id, eventId, payload, nextRevision]);
}

it("issues compatible no-store proofs through every alias with stable subjects and a seven-day cap", async () => {
  const f = fixture(), account = await f.account();
  await trial(account.user.id, new Date(now.getTime() + 14 * 86400_000));
  const subjects = new Set(), identifiers = new Set();
  for (const prefix of ["", "/api", "/v1"]) {
    const response = await f.post(account.token, prefix);
    expect(response.status, await response.clone().text()).toBe(200);
    expect(response.headers.get("Cache-Control")).toBe("no-store");
    const result = await response.json(), claims = decodeJwt(result.token);
    expect(claims).toMatchObject({ iss: "misty-hosted", aud: "misty-self-hosted", status: "eligible", schema_version: 1, iat: now.getTime() / 1000 });
    expect(claims.exp! - claims.iat!).toBe(7 * 86400);
    expect(result.expires_at).toBe(new Date(now.getTime() + 7 * 86400_000).toISOString());
    subjects.add(claims.sub); identifiers.add(claims.jti);
  }
  expect(subjects.size).toBe(1); expect(identifiers.size).toBe(3);
  await trial(account.user.id, new Date(now.getTime() + 3600_000));
  expect((await (await f.post(account.token)).json()).expires_at).toBe(new Date(now.getTime() + 3600_000).toISOString());
});

it("uses API projections for paid access and denies unpaid, expired and lifetime-only accounts", async () => {
  const f = fixture(), account = await f.account(), end = new Date(now.getTime() + 86400_000);
  await admin.query("UPDATE licenses SET tier='max',legacy_tier='max' WHERE user_id=$1", [account.user.id]);
  expect((await f.post(account.token)).status).toBe(403);
  for (const status of ["active", "trialing", "past_due", "unpaid", "canceled", "paused", "incomplete", "incomplete_expired"]) {
    await projection(account.user, status, end);
    expect((await f.post(account.token)).status).toBe(["active", "trialing"].includes(status) ? 200 : 403);
  }
  await projection(account.user, "active", now);
  expect((await f.post(account.token)).status).toBe(403);
  await trial(account.user.id, now);
  expect((await f.post(account.token)).status).toBe(403);
});

it("requires a full active account session and rejects app tokens, bad bearer precedence and untrusted origins", async () => {
  const f = fixture(), account = await f.account();
  await trial(account.user.id, new Date(now.getTime() + 86400_000));
  await admin.query("INSERT INTO user_app_installations(user_id,app_id,installed_version,granted_scopes) VALUES($1,'test','1.0.0','[]')", [account.user.id]);
  await admin.query("INSERT INTO app_runtime_sessions(token_hash,user_id,app_id,scopes,expires_at) VALUES($1,$2,'test','[]',$3)", [hashToken("app-proof-token"), account.user.id, new Date(now.getTime() + 86400_000)]);
  expect((await f.post("app-proof-token")).status).toBe(401);
  expect((await f.post("invalid", "", { Cookie: `misty_session=${account.token}` })).status).toBe(401);
  expect((await f.post(account.token, "", { Origin: "https://untrusted.invalid" })).status).toBe(403);
  expect((await f.app.request("/billing/self-host-entitlement", { method: "POST", headers: { Cookie: `misty_session=${account.token}` } })).status).toBe(200);
  await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [account.user.id]);
  expect((await f.post(account.token)).status).toBe(401);
});

it("fails closed without a signer and keeps signing errors private", async () => {
  const f = fixture(null), account = await f.account();
  expect((await f.post(account.token)).status).toBe(403);
  await trial(account.user.id, new Date(now.getTime() + 86400_000));
  const response = await f.post(account.token);
  expect(response.status).toBe(503);
  expect(await response.json()).toEqual({ code: "self_host_entitlement_unavailable" });
  const broken = fixture(async () => { throw new Error("sensitive signing information"); });
  expect(await (await broken.post(account.token)).json()).toEqual({ code: "self_host_entitlement_unavailable" });
  expect((await fixture(issuer, "self_hosted").post(account.token)).status).toBe(501);
});

it("isolates entitlement reads by account and rejects mismatched license and corrupted projection identities", async () => {
  const f = fixture(), first = await f.account(), second = await f.account();
  await projection(first.user, "active", new Date(now.getTime() + 86400_000));
  expect(await f.eligibility(second.user, now)).toBeNull();
  expect(await f.eligibility({ id: second.user.id, license_id: first.user.license_id }, now)).toBeNull();
  expect(await withTransaction(application, async (tx) => (await tx.query("SELECT user_id FROM payment_entitlement_projections WHERE user_id=$1", [first.user.id])).rowCount,
    { mode: "user", userId: second.user.id, licenseId: second.user.license_id })).toBe(0);
  await admin.query("UPDATE payment_entitlement_projections SET payload=jsonb_set(payload,'{userId}',to_jsonb($2::text)) WHERE user_id=$1", [first.user.id, second.user.id]);
  const response = await f.post(first.token);
  expect(response.status).toBe(500); expect(await response.text()).not.toContain("projection identity");
});

it("shares issuance rate limits across aliases", async () => {
  const f = fixture();
  for (let i = 0; i < 20; i++) expect((await f.post("invalid", ["", "/api", "/v1"][i % 3]!)).status).toBe(401);
  const response = await f.post("invalid");
  expect(response.status).toBe(429); expect(response.headers.get("Retry-After")).toBe("60");
});
