import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { once } from "node:events";
import type { Server } from "node:http";
import { serve } from "@hono/node-server";
import { generateKeyPair } from "jose";
import Stripe from "stripe";
import { Pool } from "pg";
import { pino } from "pino";
import { afterAll, afterEach, beforeAll, expect, it, vi } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { assertRuntimeDatabaseRole } from "../../../../../packages/database/src/roles.js";
import { createRequestBoundary } from "../../../../../packages/runtime/src/request-boundary.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { createApi } from "../../app.js";
import { createPaymentsApp } from "../../../../payments/src/app.js";
import { createBillingSummaryRepository } from "../../../../payments/src/modules/accounts/summary.js";
import { createWebhookRepository } from "../../../../payments/src/modules/webhooks/repository.js";
import { createAuthRepository } from "../auth/repository.js";
import { createAuthService, hashToken } from "../auth/service.js";
import { createPasswordHasher, type PasswordHasher } from "../auth/passwords.js";
import { createBillingSummaryClient, type BillingSummaryReader } from "../billing/summary-client.js";
import { createBillingUsage } from "./usage.js";
import { createBillingCommands } from "./commands.js";
import { createUsageRepository } from "../usage/repository.js";

const admin = createTestDatabase(), users: string[] = [], spaces: string[] = [], servers: Server[] = [], runId = randomUUID();
let application: Pool, payments: Pool, passwords: PasswordHasher;
const keys = await generateKeyPair("EdDSA", { crv: "Ed25519" }), path = "/internal/billing/summary";
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../payments/migrations/", import.meta.url))), "billing");
  await admin.query(`DO $$ BEGIN
    IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_billing_usage_api_test') THEN CREATE ROLE misty_billing_usage_api_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF;
    IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_billing_usage_payments_test') THEN CREATE ROLE misty_billing_usage_payments_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF;
  END $$;
  GRANT USAGE ON SCHEMA public TO misty_billing_usage_api_test;
  GRANT SELECT,INSERT,UPDATE,DELETE ON users,licenses,sessions TO misty_billing_usage_api_test;
  GRANT SELECT,UPDATE ON spaces,space_members TO misty_billing_usage_api_test;
  GRANT SELECT ON space_member_permission_overrides,space_storage_contributions,space_upload_reservations,space_rendition_reservations TO misty_billing_usage_api_test;
  GRANT SELECT,INSERT,UPDATE ON owner_storage_usage,space_storage_usage,hosted_ai_wallets,space_hosted_ai_wallets,hosted_ai_reservations,hosted_ai_usage_ledger TO misty_billing_usage_api_test;
  GRANT USAGE ON SCHEMA billing TO misty_billing_usage_payments_test; GRANT SELECT ON billing.account_closures TO misty_billing_usage_payments_test;
  GRANT SELECT ON billing.accounts,billing.subscriptions,billing.legacy_purchases,billing.cutover_checkpoints TO misty_billing_usage_payments_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_billing_usage_api_test", max: 4 });
  payments = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_billing_usage_payments_test", max: 4 });
  passwords = await createPasswordHasher();
}, 60000);
afterEach(async () => {
  for (const server of servers.splice(0)) { server.closeAllConnections(); await new Promise<void>((resolve) => server.close(() => resolve())); }
  await admin.query("DELETE FROM billing.subscriptions WHERE user_id=ANY($1::text[])", [users]);
  await admin.query("DELETE FROM billing.legacy_purchases WHERE user_id=ANY($1::text[])", [users]);
  await admin.query("DELETE FROM billing.accounts WHERE user_id=ANY($1::text[])", [users]);
  await withTransaction(admin, async (tx) => {
    await tx.query("DELETE FROM spaces WHERE id=ANY($1::text[])", [spaces]);
    await tx.query("DELETE FROM security_domains WHERE owner_user_id=ANY($1::text[])", [users]);
    await tx.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
  }); spaces.length = 0; users.length = 0;
  await admin.query("DELETE FROM billing.cutover_checkpoints WHERE evidence->>'billing_usage_fixture'=$1", [runId]);
});
afterAll(async () => { if (application) await application.end(); if (payments) await payments.end(); await admin.end(); });
async function completeHistory() {
  for (const name of ["legacy_purchases_imported", "legacy_subscriptions_imported"]) await admin.query("INSERT INTO billing.cutover_checkpoints(name,evidence) VALUES($1,$2)", [name, JSON.stringify({ billing_usage_fixture: runId })]);
}
async function fixture() {
  const auth = createAuthService({ repository: createAuthRepository(application), passwords, deployment: "hosted" });
  const name = `billing_usage_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
  const account = await auth.register({ username: name, email: `${name}@example.invalid`, name: "Summary account", password: "test-password", analyticsEnabled: false }); users.push(account.user.id);
  const licenseId = (await admin.query<{ license_id: string }>("SELECT license_id FROM users WHERE id=$1", [account.user.id])).rows[0]!.license_id;
  const repository = createBillingSummaryRepository(payments);
  const paymentApp = createPaymentsApp({ logger: pino({ level: "silent" }), isDraining: () => false, checkDatabase: async () => {}, migrationComplete: false,
    webhookPath: "/stripe/webhook", webhooks: { stripe: new Stripe("sk_test_billing_usage_fixture"), inbox: createWebhookRepository(payments), signingSecret: "whsec_billing_usage_fixture_only" },
    summary: { publicKeys: new Map([["summary-key", keys.publicKey]]), repository },
    commands: { publicKeys: new Map([["summary-key", keys.publicKey]]), checkout: { create: async () => { throw new Error("Not used"); } }, portal: { create: async () => { throw new Error("Not used"); } } },
  });
  const server = serve({ fetch: paymentApp.fetch, hostname: "127.0.0.1", port: 0 }) as Server; servers.push(server);
  if (!server.listening) await once(server, "listening");
  const endpoint = `http://127.0.0.1:${(server.address() as { port: number }).port}${path}`;
  const read = createBillingSummaryClient({ endpoint, keyId: "summary-key", privateKey: keys.privateKey, allowInsecureLoopback: true });
  const billing = vi.fn(read);
  const makeApi = (reader: BillingSummaryReader | null = billing, deployment: "hosted" | "self_hosted" = "hosted") => createApi({
    logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    auth: { service: auth, deployment, boundary: createRequestBoundary({ peerAddress: () => "127.0.0.1" }) },
    billing: { auth, commands: createBillingCommands({ pool: application, client: null, deployment }), usage: createBillingUsage({ pool: application, billing: reader, deployment }) },
  });
  const api = makeApi(), get = (prefix = "") => api.request(`${prefix}/billing/usage`, { headers: { Authorization: `Bearer ${account.token}` } });
  const addSubscription = async (status: string, updated = "now()") => {
    await admin.query("INSERT INTO billing.accounts(user_id,license_id,stripe_customer_id) VALUES($1,$2,$3) ON CONFLICT(user_id) DO NOTHING", [account.user.id, licenseId, `cus_${account.user.id}`]);
    await admin.query(`INSERT INTO billing.subscriptions(stripe_subscription_id,user_id,stripe_customer_id,stripe_price_id,tier,billing_interval,status,current_period_end,cancel_at_period_end,updated_at)
      VALUES($1,$2,$3,'price_summary','pro','month',$4,'2026-10-01T00:00:00Z',true,${updated})`, [`sub_${randomUUID()}`, account.user.id, `cus_${account.user.id}`, status]);
  };
  return { ...account, api, get, makeApi, paymentApp, billing, read, licenseId, addSubscription, auth };
}
async function space(owner: string, members: string[] = []) {
  const id = `space_${randomUUID()}`, domain = `domain_${id}`; spaces.push(id);
  await withTransaction(admin, async (tx) => {
    await tx.query("INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES($1,'space',$2,$3)", [domain, owner, id]);
    await tx.query("INSERT INTO spaces(id,owner_user_id,name,security_domain_id) VALUES($1,$2,$1,$3)", [id, owner, domain]);
    for (const member of new Set([owner, ...members])) await tx.query("INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,$3)", [id, member, member === owner ? "owner" : "member"]);
  });
  return id;
}
async function contribution(spaceId: string, userId: string, amount: string, state = "active") {
  const id = randomUUID();
  await admin.query("INSERT INTO space_storage_contributions(id,space_id,user_id,source_kind,source_id,logical_bytes,state) VALUES($1,$2,$3,'library_item',$1,$4,$5)", [id, spaceId, userId, amount, state]);
}
async function rendition(spaceId: string, userId: string, amount: number, state = "active") {
  const id = randomUUID();
  await admin.query("INSERT INTO space_rendition_reservations(id,space_id,user_id,source_kind,source_id,reserved_bytes,state,expires_at) VALUES($1,$2,$3,'edit',$1,$4,$5,now()+INTERVAL '1 hour')", [id, spaceId, userId, amount, state]);
}
async function upload(spaceId: string, userId: string, amount: number, state = "active") {
  const id = randomUUID();
  await admin.query(`INSERT INTO space_library_uploads(id,space_id,security_domain_id,user_id,object_key,original_filename,purpose,requested_byte_size,client_sha256,state,upload_token_hash,expires_at)
    SELECT $1,id,security_domain_id,$3,$1,'fixture.txt','library',$4,$5,'initiated',$1,now()-INTERVAL '1 minute' FROM spaces WHERE id=$2`, [id, spaceId, userId, amount, "0".repeat(64)]);
  // Storage reservations retain capacity until their owner releases them; unlike
  // AI leases, this reporting endpoint must not expire an upload independently.
  await admin.query("INSERT INTO space_upload_reservations(upload_id,space_id,user_id,reserved_bytes,state,expires_at) VALUES($1,$2,$3,$4,$5,now()-INTERVAL '1 minute')", [id, spaceId, userId, amount, state]);
}
const repository = () => createUsageRepository({ pool: application });
const reserve = (userId: string, spaceId?: string, amount = 20000n) => repository().reserve({ userId, ...(spaceId ? { spaceId } : {}), amount, meter: "assistant_ai", idempotencyKey: randomUUID(), allowPartial: false });
const wallet = async (userId: string) => (await admin.query("SELECT weekly_remaining_microusd,weekly_consumed_microusd,reserved_microusd FROM hosted_ai_wallets WHERE user_id=$1", [userId])).rows[0];

it("isolates payment storage and refuses incomplete billing history before creating wallets", async () => {
  const f = await fixture(); await assertRuntimeDatabaseRole(application, "api"); await assertRuntimeDatabaseRole(payments, "payments");
  await expect(application.query("SELECT 1 FROM billing.accounts")).rejects.toThrow("permission denied");
  await expect(payments.query("SELECT 1 FROM users")).rejects.toThrow("permission denied");
  expect((await f.get()).status).toBe(503); expect(await wallet(f.user.id)).toBeUndefined();
  expect((await f.makeApi(null).request("/billing/usage", { headers: { Authorization: `Bearer ${f.token}` } })).status).toBe(503);
});
it("preserves aliases, empty usage, account-only credentials and self-host gating", async () => {
  await completeHistory(); const f = await fixture();
  const appToken = `app_${randomUUID()}`;
  await admin.query("INSERT INTO user_app_installations(user_id,app_id,installed_version) VALUES($1,'journal','1.1.0')", [f.user.id]);
  await admin.query("INSERT INTO app_runtime_sessions(token_hash,user_id,app_id,expires_at) VALUES($1,$2,'journal',now()+INTERVAL '5 minutes')", [hashToken(appToken), f.user.id]);
  for (const prefix of ["", "/api", "/v1"]) {
    expect((await f.api.request(`${prefix}/billing/usage`, { headers: { Authorization: `Bearer ${appToken}` } })).status).toBe(401);
    expect(f.billing).not.toHaveBeenCalled();
  }
  let first: unknown;
  for (const prefix of ["", "/api", "/v1"]) {
    const response = await f.get(prefix); expect(response.status).toBe(200); expect(response.headers.get("Cache-Control")).toBe("no-store");
    const body = await response.json();
    expect(body).toMatchObject({ plan: "basic", spaces: [], storage: { owner_user_id: f.user.id, user_id: f.user.id, used_bytes: 0, reserved_bytes: 0, limit_bytes: 2000000000, remaining_bytes: 2000000000, version: 1, spaces: [] },
      personal: { ai: { used: 0, reserved: 0, limit: 150000, remaining: 150000, used_ratio: 0, available: true, paused: false } },
      agent_usage: { percentage_used: 0, plan: "basic", available: true, paused: false }, hosted_ai: { used_ratio: 0 } });
    expect(body.personal.storage).toEqual(body.storage.personal); expect(body.hosted_ai.reset_at).toBe(body.personal.ai.reset_at);
    expect(body.subscription).toBeUndefined(); expect(body.trial).toBeUndefined();
    if (first) expect(body).toEqual(first); else first = body;
  }
  expect((await admin.query("SELECT count(*) FROM hosted_ai_usage_ledger WHERE user_id=$1 AND source='weekly_grant'", [f.user.id])).rows[0].count).toBe("1");
  expect((await f.api.request("/billing/usage", { headers: { Cookie: `misty_session=${f.token}` } })).status).toBe(200);
  expect((await f.makeApi(null, "self_hosted").request("/billing/usage", { headers: { Authorization: `Bearer ${f.token}` } })).status).toBe(501);
});
it("aggregates global personal storage, uses each owner's plan, repairs only owned Space counters and retains compatibility fields", async () => {
  await completeHistory(); const f = await fixture(), other = await fixture();
  await admin.query("UPDATE licenses SET tier='pro' WHERE id=$1", [other.licenseId]);
  const owned = await space(f.user.id), joined = await space(other.user.id, [f.user.id]), former = await space(other.user.id), deleted = await space(f.user.id);
  await admin.query("UPDATE spaces SET lifecycle_state='pending_deletion' WHERE id=$1", [deleted]);
  await admin.query("UPDATE spaces SET updated_at=now()+INTERVAL '1 day' WHERE id=$1", [joined]);
  await contribution(owned, f.user.id, "100"); await contribution(owned, other.user.id, "200", "recovery"); await contribution(owned, f.user.id, "999", "released");
  await contribution(joined, f.user.id, "300"); await contribution(former, f.user.id, "400", "recovery"); await contribution(deleted, f.user.id, "888");
  await upload(owned, f.user.id, 30); await upload(owned, other.user.id, 40); await upload(owned, f.user.id, 999, "released");
  await rendition(owned, f.user.id, 50); await rendition(owned, other.user.id, 60); await rendition(former, f.user.id, 70); await rendition(joined, f.user.id, 999, "released");
  await admin.query("INSERT INTO space_storage_usage(space_id,used_bytes,reserved_bytes,version) VALUES($1,11,12,9),($2,700,80,4)", [owned, joined]);
  const response = await f.get(); expect(response.status).toBe(200); const body = await response.json();
  expect(body.storage.personal).toEqual({ used_bytes: 800, reserved_bytes: 150, limit_bytes: 2000000000, remaining_bytes: 1999999050, over_quota: false });
  expect(body.storage.spaces.map((row: {space_id: string}) => row.space_id).sort()).toEqual([owned, joined, former].sort());
  expect(body.spaces.map((row: {space_id: string}) => row.space_id)).toEqual([joined, owned]);
  const [j, o] = body.spaces;
  expect(j).toMatchObject({ role: "member", owner_user_id: other.user.id, ai: { limit: 900000 }, storage: { version: 4, space: { used_bytes: 700, reserved_bytes: 80, limit_bytes: 50000000000 } } });
  expect(o).toMatchObject({ role: "owner", owner_user_id: f.user.id, ai: { limit: 150000 }, storage: { version: 10, space: { used_bytes: 300, reserved_bytes: 180, limit_bytes: 2000000000 } } });
  for (const row of body.spaces) {
    expect(row.storage.personal).toEqual(body.storage.personal);
    for (const key of ["used_bytes", "reserved_bytes", "limit_bytes", "remaining_bytes"]) expect(row.storage[key]).toBe(body.storage.personal[key]);
  }
  expect((await (await f.get()).json()).spaces.map((row: {storage: {version: number}}) => row.storage.version)).toEqual([4, 10]);
  expect(await wallet(other.user.id)).toBeUndefined();
});
it("reclaims expired Space leases from both accounts, keeps renewed leases and reports settled consumption", async () => {
  await completeHistory(); const f = await fixture(), other = await fixture(), id = await space(f.user.id, [other.user.id]);
  const own = await reserve(f.user.id, id), theirs = await reserve(other.user.id, id), live = await reserve(f.user.id, id, 10000n);
  const spent = await reserve(f.user.id, id, 12000n);
  await repository().settle(spent.reservation, randomUUID(), { provider: "test", model: "test", inputTokens: 0n, cachedInputTokens: 0n, outputTokens: 0n, reasoningTokens: 0n, providerCost: 7000n, chargeMicrousd: 7000n });
  await admin.query("UPDATE hosted_ai_reservations SET lease_expires_at=now()-INTERVAL '1 second' WHERE id=ANY($1::text[])", [[own.reservation.id, theirs.reservation.id]]);
  await admin.query("UPDATE hosted_ai_reservations SET created_at=now()-INTERVAL '1 hour',lease_expires_at=now()+INTERVAL '10 minutes' WHERE id=$1", [live.reservation.id]);
  const response = await f.get(); expect(response.status).toBe(200); const body = await response.json();
  expect(body.personal.ai).toMatchObject({ used: 7000, reserved: 10000, limit: 150000, remaining: 133000, used_ratio: 7000/150000 });
  expect(body.spaces[0].ai).toEqual(body.personal.ai);
  expect(body.agent_usage.percentage_used).toBe(7000/150000*100);
  expect(await wallet(other.user.id)).toMatchObject({ reserved_microusd: "0", weekly_remaining_microusd: "150000" });
  expect((await admin.query("SELECT status FROM hosted_ai_reservations WHERE id=ANY($1::text[]) ORDER BY status", [[own.reservation.id, theirs.reservation.id, live.reservation.id]])).rows.map(row => row.status)).toEqual(["released", "released", "reserved"]);
});
it("preserves consumption across expired trial fallback and upgrades, then grants a due week exactly once", async () => {
  await completeHistory(); const f = await fixture(), id = await space(f.user.id);
  await admin.query("UPDATE licenses SET tier='pro',status='trialing',trial_started_at=now()-INTERVAL '15 days',expires_at=now()+INTERVAL '1 day' WHERE id=$1", [f.licenseId]);
  const spent = await reserve(f.user.id, id, 800000n);
  await repository().settle(spent.reservation, randomUUID(), { provider: "test", model: "test", inputTokens: 0n, cachedInputTokens: 0n, outputTokens: 0n, reasoningTokens: 0n, providerCost: 800000n, chargeMicrousd: 800000n });
  await f.addSubscription("active");
  const trial = await (await f.get()).json(); expect(trial.trial).toMatchObject({ status: "trialing" }); expect(trial.personal.ai.remaining).toBe(100000);
  await admin.query("UPDATE licenses SET expires_at=now()-INTERVAL '1 second' WHERE id=$1", [f.licenseId]);
  const expired = await (await f.get()).json(); expect(expired).toMatchObject({ plan: "basic", personal: { ai: { used: 150000, remaining: 0, used_ratio: 1, paused: true } }, subscription: { status: "active", billing_interval: "month", cancel_at_period_end: true } });
  expect(expired.trial).toBeUndefined(); expect(JSON.stringify(expired)).not.toContain("cus_"); expect(JSON.stringify(expired)).not.toContain("sub_");
  expect((await admin.query("SELECT tier,status,expires_at,trial_started_at FROM licenses WHERE id=$1", [f.licenseId])).rows[0]).toMatchObject({ tier: "basic", status: "active", expires_at: null, trial_started_at: expect.any(Date) });
  await admin.query("UPDATE licenses SET tier='pro' WHERE id=$1", [f.licenseId]);
  const restored = await (await f.get()).json(); expect(restored.personal.ai.remaining).toBe(100000); expect(restored.spaces[0].ai.remaining).toBe(100000);
  await admin.query("UPDATE hosted_ai_wallets SET reset_at=now()-INTERVAL '1 day' WHERE user_id=$1", [f.user.id]);
  await admin.query("UPDATE space_hosted_ai_wallets SET reset_at=now()-INTERVAL '1 day' WHERE space_id=$1", [id]);
  const reset = await (await f.get()).json(); expect(reset.personal.ai).toMatchObject({ used: 0, remaining: 900000 }); expect(reset.spaces[0].ai.remaining).toBe(900000);
  expect(await (await f.get()).json()).toEqual(reset); expect(await wallet(f.user.id)).toMatchObject({ weekly_consumed_microusd: "0" });
});
it("denies a Space permission override before any usage or storage repair is committed", async () => {
  await completeHistory(); const f = await fixture(), other = await fixture(), own = await space(f.user.id), joined = await space(other.user.id, [f.user.id]);
  await contribution(own, f.user.id, "100");
  await admin.query("INSERT INTO space_member_permission_overrides(space_id,user_id,permission,effect,updated_by_user_id) VALUES($1,$2,'storage.view_own_usage','deny',$3)", [joined, f.user.id, other.user.id]);
  const response = await f.get(); expect(response.status).toBe(500); expect(await response.text()).toBe("internal error\n");
  expect(await wallet(f.user.id)).toBeUndefined();
  expect((await admin.query("SELECT count(*) FROM space_storage_usage WHERE space_id=ANY($1::text[])", [[own, joined]])).rows[0].count).toBe("0");
});
it("rechecks sessions, membership and ownership after the private response without holding API locks over the network", async () => {
  await completeHistory(); const f = await fixture(), other = await fixture(), id = await space(f.user.id, [other.user.id]);
  f.billing.mockImplementationOnce(async (identity, signal) => {
    const result = await f.read(identity, signal);
    await admin.query("SELECT id FROM users WHERE id=$1 FOR UPDATE NOWAIT", [f.user.id]);
    await admin.query("UPDATE spaces SET owner_user_id=$2 WHERE id=$1", [id, other.user.id]);
    await admin.query("UPDATE space_members SET role=CASE WHEN user_id=$2 THEN 'owner' ELSE 'member' END WHERE space_id=$1", [id, other.user.id]);
    await admin.query("UPDATE licenses SET tier='max' WHERE id=$1", [other.licenseId]);
    return result;
  });
  expect((await (await f.get()).json()).spaces[0]).toMatchObject({ owner_user_id: other.user.id, role: "member", ai: { limit: 1800000 } });
  f.billing.mockImplementationOnce(async (identity, signal) => { const result = await f.read(identity, signal); await admin.query("DELETE FROM space_members WHERE space_id=$1 AND user_id=$2", [id, f.user.id]); return result; });
  expect((await (await f.get()).json()).spaces).toEqual([]);
  f.billing.mockImplementationOnce(async (identity, signal) => { const result = await f.read(identity, signal); await f.auth.logout(f.token); return result; });
  expect((await f.get()).status).toBe(401);
});
it("rolls back refreshed wallets and repaired storage after a late ledger failure or unsupported JSON precision", async () => {
  await completeHistory(); const f = await fixture(), id = await space(f.user.id); await contribution(id, f.user.id, "100");
  await admin.query("REVOKE INSERT ON hosted_ai_usage_ledger FROM misty_billing_usage_api_test");
  try { expect((await f.get()).status).toBe(500); }
  finally { await admin.query("GRANT INSERT ON hosted_ai_usage_ledger TO misty_billing_usage_api_test"); }
  expect(await wallet(f.user.id)).toBeUndefined();
  expect((await admin.query("SELECT count(*) FROM space_hosted_ai_wallets WHERE space_id=$1", [id])).rows[0].count).toBe("0");
  expect((await admin.query("SELECT count(*) FROM space_storage_usage WHERE space_id=$1", [id])).rows[0].count).toBe("0");
  const other = await fixture(); await contribution(id, other.user.id, "9007199254740992");
  expect((await f.get()).status).toBe(500);
  expect((await admin.query("SELECT count(*) FROM space_hosted_ai_wallets WHERE space_id=$1", [id])).rows[0].count).toBe("0");
  expect((await admin.query("SELECT count(*) FROM space_storage_usage WHERE space_id=$1", [id])).rows[0].count).toBe("0");
});
it("serializes overlapping Space/account refreshes with concurrent reservations without double releasing expired leases", async () => {
  await completeHistory(); const a = await fixture(), b = await fixture(), x = await space(a.user.id, [b.user.id]), y = await space(b.user.id, [a.user.id]);
  const old = await reserve(a.user.id, y), old2 = await reserve(b.user.id, x);
  await admin.query("UPDATE hosted_ai_reservations SET lease_expires_at=now()-INTERVAL '1 second' WHERE id=ANY($1::text[])", [[old.reservation.id, old2.reservation.id]]);
  const [first, second, live] = await Promise.all([a.get(), b.get(), reserve(a.user.id, x, 30000n)]);
  expect(first.status).toBe(200); expect(second.status).toBe(200); expect(live.reservation.amount).toBe(30000n);
  expect(await wallet(a.user.id)).toMatchObject({ reserved_microusd: "30000" }); expect(await wallet(b.user.id)).toMatchObject({ reserved_microusd: "0" });
  expect((await (await a.get()).json()).personal.ai.reserved).toBe(30000);
});
it("bounds usage admission across aliases and releases admission after request cancellation", async () => {
  await completeHistory(); const f = await fixture();
  let entered = 0, ready!: () => void, release!: () => void;
  const started = new Promise<void>(resolve => { ready = resolve; }), gate = new Promise<void>(resolve => { release = resolve; });
  f.billing.mockImplementation(async (identity, signal) => {
    const result = await f.read(identity, signal); if (++entered === 2) ready(); await gate; return result;
  });
  const controller = new AbortController();
  const pending = [f.api.request("/billing/usage", { headers: { Authorization: `Bearer ${f.token}` }, signal: controller.signal }), f.get("/api")];
  try {
    await Promise.race([started, Promise.all(pending).then(responses => { throw new Error(`Usage ended before admission gate: ${responses.map(r => r.status)}`); })]);
    expect((await f.get("/v1")).status).toBe(503); expect(entered).toBe(2); controller.abort();
  } finally { release(); }
  expect((await Promise.all(pending)).map(r => r.status)).toEqual([503, 200]);
  f.billing.mockImplementation(f.read); expect((await f.get()).status).toBe(200);
});
it("returns temporary unavailability on Space lock contention without committing partial usage", async () => {
  await completeHistory(); const f = await fixture(), id = await space(f.user.id), tx = await admin.connect();
  try {
    await tx.query("BEGIN"); await tx.query("SELECT id FROM spaces WHERE id=$1 FOR UPDATE", [id]);
    expect((await f.get()).status).toBe(503); expect(await wallet(f.user.id)).toBeUndefined();
  } finally { await tx.query("ROLLBACK"); tx.release(); }
  expect((await f.get()).status).toBe(200);
});
