import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { Hono } from "hono";
import { generateKeyPair } from "jose";
import { afterAll, afterEach, beforeAll, expect, it, vi } from "vitest";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { assertRuntimeDatabaseRole } from "../../../../../packages/database/src/roles.js";
import { signServiceAssertion } from "../../../../../packages/runtime/src/service-assertion.js";
import type { CheckoutIntent } from "../../../../../packages/service-contracts/src/payments.js";
import { createBillingSummaryRepository } from "../accounts/summary.js";
import { enqueueEntitlement } from "../entitlements/repository.js";
import { createPriceCatalog } from "../subscriptions/model.js";
import { createCheckoutRepository } from "../checkout/repository.js";
import { createCheckoutService } from "../checkout/service.js";
import { createPortalService } from "../checkout/portal.js";
import { createBillingClosureRepository } from "./repository.js";
import { createBillingClosureRoutes } from "./routes.js";
const admin = createTestDatabase(), users: string[] = [];
let payments: Pool;
const catalog = createPriceCatalog([{ id: "price_pro_month", tier: "pro", interval: "month" }]);
const urls = { success: "https://misty.example/success", cancel: "https://misty.example/cancel", portalReturn: "https://misty.example/billing" };
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../migrations/", import.meta.url))), "billing");
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_closure_test') THEN CREATE ROLE misty_closure_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA billing TO misty_closure_test;
    GRANT SELECT,INSERT,UPDATE ON billing.accounts,billing.account_closures,billing.checkout_attempts,billing.subscriptions,billing.entitlement_versions,billing.entitlement_outbox TO misty_closure_test;
    GRANT SELECT,UPDATE ON billing.legacy_checkout_recovery TO misty_closure_test;
    GRANT SELECT ON billing.cutover_checkpoints,billing.legacy_purchases TO misty_closure_test;`);
  payments = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_closure_test", application_name: "misty-closure-test", max: 5 });
});
afterEach(async () => {
  for (const table of ["account_closures", "entitlement_outbox", "entitlement_versions", "subscriptions", "checkout_attempts", "accounts"]) await admin.query(`DELETE FROM billing.${table} WHERE user_id=ANY($1::text[])`, [users]);
  users.length = 0;
  await admin.query("DELETE FROM billing.cutover_checkpoints WHERE name IN ('legacy_purchases_imported','legacy_subscriptions_imported','legacy_checkouts_imported')");
});
afterAll(async () => { if (payments) await payments.end(); await admin.end(); });
async function fixture(history = true) {
  if (history) await admin.query("INSERT INTO billing.cutover_checkpoints(name,evidence) VALUES('legacy_purchases_imported','{}'),('legacy_subscriptions_imported','{}'),('legacy_checkouts_imported','{}') ON CONFLICT DO NOTHING");
  const userId = `closure_${randomUUID()}`, licenseId = `license_${randomUUID()}`; users.push(userId);
  const intent: CheckoutIntent = { version: 1, userId, licenseId, email: "closure@example.invalid", tier: "pro", interval: "month", trialEligible: true };
  const closure = { version: 1 as const, userId, licenseId, deletionRequestId: `deletion_${randomUUID()}` };
  const gateway = { createSession: vi.fn(async () => ({ id: `cs_${userId}`, url: "https://checkout.stripe.com/test", status: "open" as const, expires_at: Math.floor(Date.now()/1000)+2100 })),
    retrieveSession: vi.fn(async () => { throw new Error("Unexpected session lookup"); }), retrieveSubscription: vi.fn(async () => { throw new Error("Unexpected subscription lookup"); }), cancelSubscription: vi.fn(async () => { throw new Error("Unexpected cancellation"); }) };
  const repository = createCheckoutRepository(payments), closures = createBillingClosureRepository(payments);
  const service = createCheckoutService({ repository, catalog, urls, gateway });
  return { intent, closure, gateway, repository, closures, service };
}
function deferred() { let resolve!: () => void; const promise = new Promise<void>(done => { resolve = done; }); return { promise, resolve }; }
async function waitingForLock() {
  for (let count = 0; count < 100; count++) {
    if ((await admin.query("SELECT 1 FROM pg_stat_activity WHERE application_name='misty-closure-test' AND wait_event_type='Lock'")).rowCount) return;
    await new Promise(done => setTimeout(done, 10));
  }
  throw new Error("Closure did not reach its account lock");
}
it("durably freezes billing without waiting for imports and deduplicates identical closure retries", async () => {
  const f = await fixture(false); await assertRuntimeDatabaseRole(payments, "payments");
  const responses = await Promise.all([f.closures.begin(f.closure), f.closures.begin(f.closure)]);
  expect(responses).toEqual([{ ...f.closure, state: "closing" }, { ...f.closure, state: "closing" }]);
  expect((await admin.query("SELECT payload FROM billing.entitlement_outbox WHERE user_id=$1", [f.intent.userId])).rows.map(row => row.payload.subscription)).toEqual([null]);
  expect((await admin.query("SELECT completed_at FROM billing.account_closures WHERE user_id=$1", [f.intent.userId])).rows[0].completed_at).toBeNull();
  await expect(payments.query("SELECT * FROM public.users")).rejects.toThrow("permission denied");
});
it("rejects conflicting license, deletion ID and cross-account request reuse without a partial tombstone", async () => {
  const f = await fixture(), other = await fixture(); await f.closures.begin(f.closure);
  await expect(f.closures.begin({ ...f.closure, licenseId: "wrong" })).rejects.toThrow();
  await expect(f.closures.begin({ ...f.closure, deletionRequestId: "wrong" })).rejects.toThrow();
  await expect(other.closures.begin({ ...other.closure, deletionRequestId: f.closure.deletionRequestId })).rejects.toThrow();
  expect((await admin.query("SELECT * FROM billing.accounts WHERE user_id=$1", [other.intent.userId])).rowCount).toBe(0);
  expect((await admin.query("SELECT * FROM billing.account_closures WHERE user_id=$1", [f.intent.userId])).rowCount).toBe(1);
});
it("blocks new and previously prepared checkouts after closure without calling Stripe", async () => {
  const f = await fixture(), attempt = await f.repository.begin(f.intent, catalog, urls); if ("legacyUrl" in attempt) throw new Error("Unexpected legacy session");
  await f.closures.begin(f.closure);
  await expect(f.service.create(f.intent)).rejects.toMatchObject({ code: "billing_account_closed" });
  await expect(f.repository.prepareReplacement(attempt, catalog, { retrieve: f.gateway.retrieveSubscription, cancel: f.gateway.cancelSubscription })).rejects.toMatchObject({ code: "billing_account_closed" });
  const operation = vi.fn(); await expect(f.repository.withOpenAttempt(attempt, operation)).rejects.toMatchObject({ code: "billing_account_closed" });
  expect(operation).not.toHaveBeenCalled(); expect(f.gateway.createSession).not.toHaveBeenCalled();
});
it("holds the account lock through an in-flight checkout result before closure can commit", async () => {
  const f = await fixture(), entered = deferred(), release = deferred();
  f.gateway.createSession.mockImplementationOnce(async () => { entered.resolve(); await release.promise; return { id: "cs_inflight", url: "https://checkout.stripe.com/inflight", status: "open", expires_at: Math.floor(Date.now()/1000)+2100 }; });
  const creating = f.service.create(f.intent); await entered.promise;
  let finished = false; const closing = f.closures.begin(f.closure).then(value => { finished = true; return value; });
  try { await waitingForLock(); expect(finished).toBe(false); } finally { release.resolve(); }
  expect(await creating).toBe("https://checkout.stripe.com/inflight"); expect(await closing).toMatchObject({ state: "closing" });
  expect((await admin.query("SELECT status,stripe_checkout_session_id FROM billing.checkout_attempts WHERE user_id=$1", [f.intent.userId])).rows[0]).toEqual({ status: "open", stripe_checkout_session_id: "cs_inflight" });
  await expect(f.service.create(f.intent)).rejects.toMatchObject({ code: "billing_account_closed" }); expect(f.gateway.createSession).toHaveBeenCalledTimes(1);
});
it("retains an unresolved create after response loss and forbids another create once closed", async () => {
  const f = await fixture(); f.gateway.createSession.mockRejectedValueOnce(new Error("response lost after external creation"));
  await expect(f.service.create(f.intent)).rejects.toThrow("response lost");
  await f.closures.begin(f.closure); await expect(f.service.create(f.intent)).rejects.toMatchObject({ code: "billing_account_closed" });
  expect(f.gateway.createSession).toHaveBeenCalledTimes(1);
  expect((await admin.query("SELECT status,stripe_checkout_session_id FROM billing.checkout_attempts WHERE user_id=$1", [f.intent.userId])).rows[0]).toEqual({ status: "creating", stripe_checkout_session_id: null });
});
it("serializes portal creation with closure and prevents later portal sessions", async () => {
  const f = await fixture(); await f.repository.begin(f.intent, catalog, urls);
  await admin.query("UPDATE billing.accounts SET stripe_customer_id='cus_closure_portal' WHERE user_id=$1", [f.intent.userId]);
  const entered = deferred(), release = deferred(), createSession = vi.fn(async () => { entered.resolve(); await release.promise; return { url: "https://billing.stripe.com/test" }; });
  const portal = createPortalService({ pool: payments, returnUrl: urls.portalReturn, createSession });
  const creating = portal.create(f.intent.userId, f.intent.licenseId, randomUUID()); await entered.promise;
  const closing = f.closures.begin(f.closure);
  try { await waitingForLock(); } finally { release.resolve(); }
  expect(await creating).toBe("https://billing.stripe.com/test"); await closing;
  await expect(portal.create(f.intent.userId, f.intent.licenseId, randomUUID())).rejects.toMatchObject({ code: "billing_account_closed" }); expect(createSession).toHaveBeenCalledTimes(1);
});
it("suppresses late paid projections while retaining ordered delivery evidence", async () => {
  const f = await fixture(); await f.closures.begin(f.closure);
  await admin.query("UPDATE billing.account_closures SET state='closed',completed_at=now() WHERE user_id=$1", [f.intent.userId]);
  const event = await withTransaction(payments, async tx => { await tx.query("SELECT user_id FROM billing.accounts WHERE user_id=$1 FOR UPDATE", [f.intent.userId]); return enqueueEntitlement(tx, { userId: f.intent.userId, licenseId: f.intent.licenseId,
    subscription: { subscriptionId: "sub_late", tier: "pro", interval: "month", status: "active", currentPeriodEnd: new Date(Date.now()+86400000).toISOString(), cancelAtPeriodEnd: false } }); });
  expect(event.subscription).toBeNull(); expect(event.revision).toBe("2");
  expect((await admin.query("SELECT state,completed_at,last_error_code FROM billing.account_closures WHERE user_id=$1", [f.intent.userId])).rows[0]).toEqual({ state: "closing", completed_at: null, last_error_code: "late_billing_activity" });
  await admin.query("INSERT INTO billing.subscriptions(stripe_subscription_id,user_id,stripe_customer_id,stripe_price_id,tier,billing_interval,status) VALUES('sub_closing_summary',$1,'cus_closing_summary','price_pro_month','pro','month','canceled')", [f.intent.userId]);
  expect((await createBillingSummaryRepository(payments).get({ version: 1, userId: f.intent.userId, licenseId: f.intent.licenseId })).subscription?.customerPortalAvailable).toBe(false);
});
it("accepts only signed, body-bound and account-bound lifecycle commands", async () => {
  const f = await fixture(), keys = await generateKeyPair("EdDSA", { crv: "Ed25519" }), path = "/internal/billing/account-closure";
  const app = new Hono().route(path, createBillingClosureRoutes({ publicKeys: new Map([["api-key", keys.publicKey]]), repository: f.closures }));
  const body = JSON.stringify(f.closure);
  const sign = (subject = f.intent.userId, scope = "billing:account-closure", value = body) => signServiceAssertion({ privateKey: keys.privateKey, keyId: "api-key", issuer: "misty-api", audience: "misty-payments", subject, scope, request: { method: "POST", path, body: Buffer.from(value) } });
  const send = (token = "app-token", value = body) => app.request(path, { method: "POST", headers: { Authorization: `Bearer ${token}` }, body: value });
  expect((await send()).status).toBe(401); expect((await send(await sign("other"))).status).toBe(403); expect((await send(await sign(f.intent.userId, "billing:checkout"))).status).toBe(401);
  expect((await send(await sign(), body + " ")).status).toBe(401);
  const injected = JSON.stringify({ ...f.closure, stripeCustomerId: "cus_attacker" }); expect((await send(await sign(f.intent.userId, "billing:account-closure", injected), injected)).status).toBe(400);
  const response = await send(await sign()); expect(response.status).toBe(200); expect(response.headers.get("Cache-Control")).toBe("no-store"); expect(await response.json()).toEqual({ ...f.closure, state: "closing" });
});
it("rolls back a new tombstone and account when entitlement publication cannot be recorded", async () => {
  const f = await fixture();
  await admin.query("REVOKE INSERT ON billing.entitlement_outbox FROM misty_closure_test");
  try {
    await expect(f.closures.begin(f.closure)).rejects.toThrow("permission denied");
    expect((await admin.query("SELECT * FROM billing.account_closures WHERE user_id=$1", [f.intent.userId])).rowCount).toBe(0);
    expect((await admin.query("SELECT * FROM billing.accounts WHERE user_id=$1", [f.intent.userId])).rowCount).toBe(0);
  } finally { await admin.query("GRANT INSERT ON billing.entitlement_outbox TO misty_closure_test"); }
  expect(await f.closures.begin(f.closure)).toMatchObject({ state: "closing" });
});
it("bounds closure lock contention without committing a partial request", async () => {
  const f = await fixture(); await f.repository.begin(f.intent, catalog, urls);
  const lock = await admin.connect();
  try {
    await lock.query("BEGIN"); await lock.query("SELECT user_id FROM billing.accounts WHERE user_id=$1 FOR UPDATE", [f.intent.userId]);
    await expect(f.closures.begin(f.closure)).rejects.toBeInstanceOf((await import("./repository.js")).BillingClosureUnavailable);
  } finally { await lock.query("ROLLBACK"); lock.release(); }
  expect((await admin.query("SELECT * FROM billing.account_closures WHERE user_id=$1", [f.intent.userId])).rowCount).toBe(0);
});
