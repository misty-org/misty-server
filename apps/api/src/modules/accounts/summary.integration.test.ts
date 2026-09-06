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
import { signServiceAssertion } from "../../../../../packages/runtime/src/service-assertion.js";
import { createApi } from "../../app.js";
import { createPaymentsApp } from "../../../../payments/src/app.js";
import { createBillingSummaryRepository } from "../../../../payments/src/modules/accounts/summary.js";
import { createWebhookRepository } from "../../../../payments/src/modules/webhooks/repository.js";
import { createAuthRepository } from "../auth/repository.js";
import { createAuthService, hashToken } from "../auth/service.js";
import { createPasswordHasher, type PasswordHasher } from "../auth/passwords.js";
import { createBillingSummaryClient, type BillingSummaryReader } from "../billing/summary-client.js";
import { createAccountRepository } from "./repository.js";
import { createAccountSummary } from "./summary.js";

const admin = createTestDatabase(), users: string[] = [], servers: Server[] = [], runId = randomUUID();
let application: Pool, payments: Pool, passwords: PasswordHasher;
const keys = await generateKeyPair("EdDSA", { crv: "Ed25519" }), path = "/internal/billing/summary";
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../payments/migrations/", import.meta.url))), "billing");
  await admin.query(`DO $$ BEGIN
    IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_summary_api_test') THEN CREATE ROLE misty_summary_api_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF;
    IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_summary_payments_test') THEN CREATE ROLE misty_summary_payments_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF;
  END $$;
  GRANT USAGE ON SCHEMA public TO misty_summary_api_test;
  GRANT SELECT,INSERT,UPDATE,DELETE ON users,licenses,sessions TO misty_summary_api_test;
  GRANT SELECT,UPDATE ON self_host_accounts TO misty_summary_api_test;
  GRANT USAGE ON SCHEMA billing TO misty_summary_payments_test; GRANT SELECT ON billing.account_closures TO misty_summary_payments_test;
  GRANT SELECT ON billing.accounts,billing.subscriptions,billing.legacy_purchases,billing.cutover_checkpoints TO misty_summary_payments_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_summary_api_test", max: 4 });
  payments = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_summary_payments_test", max: 4 });
  passwords = await createPasswordHasher();
}, 60000);
afterEach(async () => {
  for (const server of servers.splice(0)) { server.closeAllConnections(); await new Promise<void>((resolve) => server.close(() => resolve())); }
  await admin.query("DELETE FROM billing.subscriptions WHERE user_id=ANY($1::text[])", [users]);
  await admin.query("DELETE FROM billing.legacy_purchases WHERE user_id=ANY($1::text[])", [users]);
  await admin.query("DELETE FROM billing.accounts WHERE user_id=ANY($1::text[])", [users]);
  await admin.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]); users.length = 0;
  await admin.query("DELETE FROM billing.cutover_checkpoints WHERE evidence->>'summary_fixture'=$1", [runId]);
});
afterAll(async () => { if (application) await application.end(); if (payments) await payments.end(); await admin.end(); });
async function completeHistory() {
  for (const name of ["legacy_purchases_imported", "legacy_subscriptions_imported"]) await admin.query("INSERT INTO billing.cutover_checkpoints(name,evidence) VALUES($1,$2)", [name, JSON.stringify({ summary_fixture: runId })]);
}
async function fixture() {
  const auth = createAuthService({ repository: createAuthRepository(application), passwords, deployment: "hosted" });
  const name = `summary_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
  const account = await auth.register({ username: name, email: `${name}@example.invalid`, name: "Summary account", password: "test-password", analyticsEnabled: false }); users.push(account.user.id);
  const licenseId = (await admin.query<{ license_id: string }>("SELECT license_id FROM users WHERE id=$1", [account.user.id])).rows[0]!.license_id;
  const repository = createBillingSummaryRepository(payments);
  const paymentApp = createPaymentsApp({ logger: pino({ level: "silent" }), isDraining: () => false, checkDatabase: async () => {}, migrationComplete: false,
    webhookPath: "/stripe/webhook", webhooks: { stripe: new Stripe("sk_test_summary_fixture"), inbox: createWebhookRepository(payments), signingSecret: "whsec_summary_fixture_only" },
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
    auth: { service: auth, deployment: "hosted", boundary: createRequestBoundary({ peerAddress: () => "127.0.0.1" }) },
    accounts: { auth, repository: createAccountRepository(application), summary: createAccountSummary({ pool: application, billing: reader, deployment }) },
  });
  const api = makeApi(), get = (prefix = "") => api.request(`${prefix}/me`, { headers: { Authorization: `Bearer ${account.token}` } });
  const addSubscription = async (status: string, updated = "now()") => {
    await admin.query("INSERT INTO billing.accounts(user_id,license_id,stripe_customer_id) VALUES($1,$2,$3) ON CONFLICT(user_id) DO NOTHING", [account.user.id, licenseId, `cus_${account.user.id}`]);
    await admin.query(`INSERT INTO billing.subscriptions(stripe_subscription_id,user_id,stripe_customer_id,stripe_price_id,tier,billing_interval,status,current_period_end,cancel_at_period_end,updated_at)
      VALUES($1,$2,$3,'price_summary','pro','month',$4,'2026-10-01T00:00:00Z',true,${updated})`, [`sub_${randomUUID()}`, account.user.id, `cus_${account.user.id}`, status]);
  };
  return { ...account, api, get, makeApi, paymentApp, billing, read, licenseId, addSubscription };
}

it("keeps both database roles isolated and refuses to infer missing payment history", async () => {
  const f = await fixture(); await assertRuntimeDatabaseRole(application, "api"); await assertRuntimeDatabaseRole(payments, "payments");
  await expect(application.query("SELECT 1 FROM billing.accounts")).rejects.toThrow("permission denied");
  await expect(payments.query("SELECT 1 FROM users")).rejects.toThrow("permission denied");
  await expect(payments.query("INSERT INTO billing.cutover_checkpoints(name,evidence) VALUES('forged','{}')")).rejects.toThrow("permission denied");
  expect((await f.get()).status).toBe(503);
  await admin.query("INSERT INTO billing.cutover_checkpoints(name,evidence) VALUES('legacy_purchases_imported',$1)", [JSON.stringify({ summary_fixture: runId })]);
  expect((await f.get()).status).toBe(503);
  expect((await f.makeApi(null).request("/me", { headers: { Authorization: `Bearer ${f.token}` } })).status).toBe(503);
});
it("preserves the complete account summary through every alias without exposing private payment identities", async () => {
  await completeHistory(); const f = await fixture();
  const createdAt = (await admin.query("SELECT created_at FROM users WHERE id=$1", [f.user.id])).rows[0].created_at.toISOString();
  for (const prefix of ["", "/api", "/v1"]) {
    const response = await f.get(prefix); expect(response.status).toBe(200); expect(response.headers.get("Cache-Control")).toBe("no-store");
    expect(await response.json()).toEqual({ id: f.user.id, name: "Summary account", username: f.user.username, email: f.user.email, avatar_version: 0, created_at: createdAt,
      tier: "basic", status: "active", allows_use: true, expires_at: null, trial_started_at: null, trial_eligible: true, license_device: "",
      billing: { kind: "free", interval: null, subscription_status: null, current_period_end: null, cancel_at_period_end: false, customer_portal_available: false } });
  }
  await f.addSubscription("active"); await f.addSubscription("canceled", "now()+interval '1 day'");
  const paid = await (await f.get()).json();
  expect(paid.billing).toEqual({ kind: "subscription", interval: "month", subscription_status: "active", current_period_end: "2026-10-01T00:00:00.000Z", cancel_at_period_end: true, customer_portal_available: true });
  expect(paid.trial_eligible).toBe(false); expect(JSON.stringify(paid)).not.toContain("cus_"); expect(JSON.stringify(paid)).not.toContain("sub_");
  // Payment metadata cannot grant access before the separate entitlement event.
  expect(paid.tier).toBe("basic");
});
it("rejects runtime roles that can forge migration history, completion markers or original import snapshots", async () => {
  for (const table of ["billing.cutover_checkpoints", "billing.legacy_subscription_records", "billing.goose_db_version", "billing.misty_migration_checksums"]) {
    await admin.query(`GRANT INSERT ON ${table} TO misty_summary_payments_test`);
    try { await expect(assertRuntimeDatabaseRole(payments, "payments")).rejects.toThrow("violates service isolation"); }
    finally { await admin.query(`REVOKE INSERT ON ${table} FROM misty_summary_payments_test`); }
  }
  await admin.query("GRANT UPDATE ON public.misty_migration_checksums TO misty_summary_api_test");
  try { await expect(assertRuntimeDatabaseRole(application, "api")).rejects.toThrow("violates service isolation"); }
  finally { await admin.query("REVOKE UPDATE ON public.misty_migration_checksums FROM misty_summary_api_test"); }
  await assertRuntimeDatabaseRole(application, "api"); await assertRuntimeDatabaseRole(payments, "payments");
});
it("preserves trial history, lifetime grants, canceled subscriptions and completed-purchase eligibility rules", async () => {
  await completeHistory(); const f = await fixture();
  await admin.query("UPDATE licenses SET status='trialing',tier='pro',trial_started_at=now() WHERE id=$1", [f.licenseId]);
  expect(await (await f.get()).json()).toMatchObject({ trial_eligible: false, tier: "pro", billing: { kind: "trial" } });
  await admin.query("UPDATE licenses SET status='active',trial_started_at=NULL,legacy_tier='max' WHERE id=$1", [f.licenseId]);
  expect(await (await f.get()).json()).toMatchObject({ trial_eligible: false, billing: { kind: "lifetime" } });
  await admin.query("UPDATE licenses SET legacy_tier=NULL WHERE id=$1", [f.licenseId]);
  await f.addSubscription("canceled");
  expect(await (await f.get()).json()).toMatchObject({ trial_eligible: false, billing: { kind: "free", subscription_status: "canceled", customer_portal_available: true } });
  await admin.query("DELETE FROM billing.subscriptions WHERE user_id=$1", [f.user.id]);
  await admin.query(`INSERT INTO billing.legacy_purchases(id,user_id,license_id,tier_purchased,stripe_checkout_session_id,status,created_at,updated_at)
    VALUES($1,$2,$3,'personal',$4,'completed',now(),now())`, [randomUUID(), f.user.id, f.licenseId, `cs_${randomUUID()}`]);
  expect(await (await f.get()).json()).toMatchObject({ trial_eligible: false });
  await admin.query("UPDATE billing.legacy_purchases SET status='refunded' WHERE user_id=$1", [f.user.id]);
  expect(await (await f.get()).json()).toMatchObject({ trial_eligible: true });
});
it("requires signed subject, body and action binding on the private route even with checkout routes mounted", async () => {
  await completeHistory(); const f = await fixture(), body = JSON.stringify({ version: 1, userId: f.user.id, licenseId: f.licenseId });
  const sign = (subject = f.user.id, scope = "billing:summary", value = body) => signServiceAssertion({ privateKey: keys.privateKey, keyId: "summary-key", issuer: "misty-api", audience: "misty-payments", subject, scope, request: { method: "POST", path, body: Buffer.from(value) } });
  const send = (token: string, value = body) => f.paymentApp.request(path, { method: "POST", body: value, headers: { Authorization: `Bearer ${token}` } });
  expect((await send(f.token)).status).toBe(401); expect((await send("app-token")).status).toBe(401);
  expect((await send(await sign("another-account"))).status).toBe(403);
  expect((await send(await sign(f.user.id, "billing:checkout"))).status).toBe(401);
  expect((await send(await sign(), `${body} `)).status).toBe(401);
  const injected = JSON.stringify({ version: 1, userId: f.user.id, licenseId: f.licenseId, customerId: "injected" });
  expect((await send(await sign(f.user.id, "billing:summary", injected), injected)).status).toBe(400);
  expect((await send(await sign())).status).toBe(200);
  await f.addSubscription("active"); await admin.query("UPDATE billing.accounts SET license_id='other-license' WHERE user_id=$1", [f.user.id]);
  expect((await send(await sign())).status).toBe(409); expect((await f.get()).status).toBe(503);
});
it("rechecks the account session after the payment response and denies App credentials before contacting payments", async () => {
  await completeHistory(); const f = await fixture();
  expect((await f.api.request("/me", { headers: { Authorization: "Bearer app-token" } })).status).toBe(401); expect(f.billing).not.toHaveBeenCalled();
  f.billing.mockImplementationOnce(async (identity, signal) => { const history = await f.read(identity, signal); await admin.query("DELETE FROM sessions WHERE token_hash=$1", [hashToken(f.token)]); return history; });
  expect((await f.get()).status).toBe(401);
  expect(f.billing).toHaveBeenCalledTimes(1);
  const other = await fixture();
  other.billing.mockImplementationOnce(async (identity, signal) => { const history = await other.read(identity, signal); await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [other.user.id]); return history; });
  expect((await other.get()).status).toBe(401);
  expect(other.billing).toHaveBeenCalledTimes(1);
});
it("serves self-host summaries without a payments client while checking current local entitlement", async () => {
  const f = await fixture(), api = f.makeApi(null, "self_hosted");
  const get = () => api.request("/me", { headers: { Authorization: `Bearer ${f.token}` } });
  expect((await get()).status).toBe(401);
  await admin.query("INSERT INTO self_host_accounts(user_id,entitlement_subject,entitlement_expires_at) VALUES($1,$2,now()+interval '1 day')", [f.user.id, `summary-${randomUUID()}`]);
  expect(await (await get()).json()).toMatchObject({ id: f.user.id, billing: { kind: "free", customer_portal_available: false } });
  expect(f.billing).not.toHaveBeenCalled();
  await admin.query("UPDATE self_host_accounts SET disabled_at=now() WHERE user_id=$1", [f.user.id]);
  expect((await get()).status).toBe(401);
});
