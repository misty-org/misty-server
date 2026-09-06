import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { afterAll, afterEach, beforeAll, expect, it, vi } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { createPriceCatalog } from "../subscriptions/model.js";
import { createBillingClosureRepository } from "./repository.js";
import { createBillingClosureWorker } from "./worker.js";
import type { ClosureGateway } from "./model.js";
const admin = createTestDatabase(), users: string[] = [], legacyIds: string[] = [];
let payments: Pool;
const catalog = createPriceCatalog([{ id: "price_pro_month", tier: "pro", interval: "month" }]);
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../migrations/", import.meta.url))), "billing");
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_closure_worker_test') THEN CREATE ROLE misty_closure_worker_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA billing TO misty_closure_worker_test;
    GRANT SELECT,INSERT,UPDATE ON billing.accounts,billing.account_closures,billing.account_closure_resources,billing.checkout_attempts,billing.subscriptions,billing.entitlement_versions,billing.entitlement_outbox TO misty_closure_worker_test;
    GRANT SELECT,UPDATE ON billing.legacy_checkout_recovery TO misty_closure_worker_test;
    GRANT SELECT ON billing.legacy_purchases,billing.cutover_checkpoints TO misty_closure_worker_test;`);
  payments = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_closure_worker_test", application_name: "misty-closure-worker-test", max: 4 });
});
afterEach(async () => {
  for (const table of ["account_closures", "entitlement_outbox", "entitlement_versions", "subscriptions", "checkout_attempts", "legacy_purchases", "legacy_checkout_recovery", "accounts"]) await admin.query(`DELETE FROM billing.${table} WHERE user_id=ANY($1::text[])`, [users]);
  await admin.query("DELETE FROM billing.legacy_checkout_records WHERE id=ANY($1::text[])", [legacyIds]);
  await admin.query("DELETE FROM billing.cutover_checkpoints WHERE name IN ('legacy_purchases_imported','legacy_subscriptions_imported','legacy_checkouts_imported')");
  users.length = 0; legacyIds.length = 0;
});
afterAll(async () => { if (payments) await payments.end(); await admin.end(); });
async function fixture(primary = true) {
  const userId = `closure_${randomUUID()}`, licenseId = `license_${randomUUID()}`, customerId = `cus_${randomUUID()}`; users.push(userId);
  const customers = new Map<string, { id: string; deleted?: boolean; metadata: { user_id: string; license_id: string } }>();
  const sessions = new Map<string, any>(), subscriptions = new Map<string, any>();
  const addCustomer = (id = customerId) => { customers.set(id, { id, metadata: { user_id: userId, license_id: licenseId } }); return id; };
  if (primary) addCustomer();
  await admin.query("INSERT INTO billing.accounts(user_id,license_id,stripe_customer_id) VALUES($1,$2,$3)", [userId, licenseId, primary ? customerId : null]);
  const get = (map: Map<string, unknown>, id: string) => { const value = map.get(id); if (!value) throw new Error("Provider object unavailable"); return structuredClone(value); };
  const page = (map: Map<string, any>, query: { customer: string; starting_after?: string }) => {
    const values = [...map.values()].filter(value => value.customer === query.customer).sort((a, b) => a.id.localeCompare(b.id));
    const start = query.starting_after ? values.findIndex(value => value.id === query.starting_after)+1 : 0;
    const data = values.slice(start, start+100); return { data: structuredClone(data), has_more: values.length > start+100 };
  };
  let loseExpire = false, loseCancel = false, loseDelete = false;
  const gateway = {
    retrieveSession: vi.fn(async (id: string) => get(sessions, id)),
    expireSession: vi.fn(async (id: string) => { const session = sessions.get(id); if (session.status !== "open") throw new Error("Not expireable"); session.status = "expired";
      if (loseExpire) { loseExpire = false; throw new Error("lost response containing provider-private-data"); } return structuredClone(session); }),
    retrieveSubscription: vi.fn(async (id: string) => get(subscriptions, id)),
    cancelSubscription: vi.fn(async (id: string) => { const subscription = subscriptions.get(id); if (subscription.status === "canceled") throw new Error("Already canceled"); subscription.status = "canceled";
      if (loseCancel) { loseCancel = false; throw new Error("lost response containing provider-private-data"); } return structuredClone(subscription); }),
    retrieveCustomer: vi.fn(async (id: string) => get(customers, id)),
    deleteCustomer: vi.fn(async (id: string) => { const customer = customers.get(id)!; customer.deleted = true;
      if (loseDelete) { loseDelete = false; throw new Error("lost response containing provider-private-data"); } return { id, deleted: true }; }),
    listSessions: vi.fn(async (query: { customer: string; limit: 100; starting_after?: string }) => page(sessions, query)),
    listSubscriptions: vi.fn(async (query: { customer: string; status: "all"; limit: 100; starting_after?: string }) => page(subscriptions, query)),
  } satisfies ClosureGateway;
  const closures = createBillingClosureRepository(payments), worker = createBillingClosureWorker({ pool: payments, catalog, gateway });
  const intent = { version: 1 as const, userId, licenseId, deletionRequestId: `deletion_${randomUUID()}` };
  const state = async () => (await admin.query("SELECT state,last_error_code,attempts FROM billing.account_closures WHERE user_id=$1", [userId])).rows[0];
  const ready = () => admin.query("UPDATE billing.account_closures SET available_at=clock_timestamp() WHERE user_id=$1", [userId]);
  const begin = async (history = true) => {
    if (history) await admin.query("INSERT INTO billing.cutover_checkpoints(name,evidence) VALUES('legacy_purchases_imported','{}'),('legacy_subscriptions_imported','{}'),('legacy_checkouts_imported','{}') ON CONFLICT DO NOTHING");
    await closures.begin(intent);
  };
  const drain = async (maximum = 30) => { for (let count = 0; count < maximum; count++) { await worker.runOnce(); const current = await state(); if (current.state === "closed") return;
    if (current.last_error_code) throw new Error(current.last_error_code); } throw new Error("Cleanup did not finish within its resource count"); };
  async function session(source: "native" | "legacy" | "purchase" | "discovered" = "native", customer = customerId, status = "open") {
    const id = `cs_${randomUUID()}`, attempt = randomUUID();
    const value = { id, customer, subscription: null, payment_intent: `pi_${randomUUID()}`, status, mode: source === "purchase" ? "payment" : "subscription", expires_at: Math.floor(Date.now()/1000)+2100,
      client_reference_id: userId, metadata: { user_id: userId, license_id: licenseId, kind: "subscription", tier: "pro", interval: "month", ...(source === "native" ? { checkout_attempt_id: attempt } : {}) }, after_expiration: null };
    sessions.set(id, value);
    if (source === "native") await admin.query(`INSERT INTO billing.checkout_attempts(id,user_id,license_id,tier,billing_interval,status,stripe_checkout_session_id,stripe_parameters,expires_at)
      VALUES($1,$2,$3,'pro','month',$4,$5,$6,now()+interval '35 minutes')`, [attempt, userId, licenseId, status === "complete" ? "completed" : status, id, { customer, customer_email: "private@example.invalid" }]);
    if (source === "legacy") {
      legacyIds.push(attempt); await admin.query("INSERT INTO billing.legacy_checkout_records(id,source_record) VALUES($1,'{}')", [attempt]);
      await admin.query(`INSERT INTO billing.legacy_checkout_recovery(id,user_id,license_id,tier,billing_interval,source_status,source_session_id,source_created_at,source_expires_at)
        VALUES($1,$2,$3,'pro','month','open',$4,now(),now()+interval '35 minutes')`, [attempt, userId, licenseId, id]);
    }
    if (source === "purchase") await admin.query(`INSERT INTO billing.legacy_purchases(id,user_id,license_id,tier_purchased,stripe_checkout_session_id,stripe_payment_intent_id,stripe_customer_id,status,created_at,updated_at)
      VALUES($1,$2,$3,'pro',$4,$5,$6,'completed',now(),now())`, [attempt, userId, licenseId, id, value.payment_intent, customer]);
    return value;
  }
  async function subscription(status = "active", customer = customerId, local = true) {
    const id = `sub_${randomUUID()}`, value = { id, customer, metadata: { user_id: userId, license_id: licenseId, kind: "subscription", tier: "pro", interval: "month" }, status,
      current_period_end: Math.floor(Date.now()/1000)+86400, canceled_at: null, cancel_at_period_end: false, items: { data: [{ price: { id: "price_pro_month", recurring: { interval: "month" } } }] } };
    subscriptions.set(id, value);
    if (local) await admin.query("INSERT INTO billing.subscriptions(stripe_subscription_id,user_id,stripe_customer_id,stripe_price_id,tier,billing_interval,status,current_period_end) VALUES($1,$2,$3,'price_pro_month','pro','month',$4,now()+interval '1 day')", [id, userId, customer, status]);
    return value;
  }
  return { userId, licenseId, customerId, customers, sessions, subscriptions, addCustomer, session, subscription, gateway, worker, closures, intent, begin, state, ready, drain,
    lose: (operation: "expire" | "cancel" | "delete") => { if (operation === "expire") loseExpire = true; if (operation === "cancel") loseCancel = true; if (operation === "delete") loseDelete = true; } };
}
it("expires native checkouts, cancels subscriptions and verifies customer deletion before closing", async () => {
  const f = await fixture(); const session = await f.session(), subscription = await f.subscription(); await f.begin(); await f.drain();
  expect(session.status).toBe("expired"); expect(subscription.status).toBe("canceled"); expect(f.customers.get(f.customerId)?.deleted).toBe(true);
  expect(f.gateway.expireSession).toHaveBeenCalledTimes(1); expect(f.gateway.cancelSubscription).toHaveBeenCalledTimes(1); expect(f.gateway.deleteCustomer).toHaveBeenCalledTimes(1);
  expect((await admin.query("SELECT stripe_parameters,checkout_url FROM billing.checkout_attempts WHERE user_id=$1", [f.userId])).rows[0]).toEqual({ stripe_parameters: {}, checkout_url: "" });
  expect(await f.closures.begin(f.intent)).toEqual({ ...f.intent, state: "closed" }); expect(await f.worker.runOnce()).toBe(false);
  expect((await admin.query("SELECT state FROM billing.account_closure_resources WHERE user_id=$1", [f.userId])).rows.every(row => row.state === "completed")).toBe(true);
  expect((await admin.query("SELECT payload FROM billing.entitlement_outbox WHERE user_id=$1", [f.userId])).rows.every(row => row.payload.subscription === null)).toBe(true);
});
it("verifies and expires a known legacy session before resolving its recovery record", async () => {
  const f = await fixture(); await f.session("legacy"); await f.begin(); await f.drain();
  expect((await admin.query("SELECT state,status,checkout_url FROM billing.legacy_checkout_recovery WHERE user_id=$1", [f.userId])).rows[0]).toEqual({ state: "verified", status: "expired", checkout_url: "" });
});
it("discovers historical customers and unimported subscriptions from verified one-time purchases", async () => {
  const f = await fixture(), old = f.addCustomer(`cus_${randomUUID()}`); await f.session("purchase", old, "complete"); const sub = await f.subscription("active", old, false);
  await f.begin(); await f.drain(); expect(sub.status).toBe("canceled"); expect(f.customers.get(old)?.deleted).toBe(true); expect(f.gateway.deleteCustomer).toHaveBeenCalledTimes(2);
});
it.each(["expire", "cancel", "delete"] as const)("recovers a lost %s response from canonical provider state without repeating the mutation", async operation => {
  const f = await fixture(); if (operation === "expire") await f.session(); if (operation === "cancel") await f.subscription(); f.lose(operation); await f.begin();
  for (let count = 0; count < 10 && !(await f.state()).last_error_code; count++) await f.worker.runOnce();
  expect(await f.state()).toMatchObject({ state: "closing", last_error_code: "closure_provider_unavailable", attempts: 1 }); expect(await f.worker.runOnce()).toBe(false);
  await f.ready(); await f.drain(); const calls = operation === "expire" ? f.gateway.expireSession : operation === "cancel" ? f.gateway.cancelSubscription : f.gateway.deleteCustomer;
  expect(calls).toHaveBeenCalledTimes(1);
});
it("waits for historical import and unresolved creation evidence instead of declaring an empty account closed", async () => {
  const f = await fixture(false); await f.begin(false); await f.worker.runOnce(); expect((await f.state()).last_error_code).toBe("closure_history_unavailable");
  await f.begin(); await admin.query("INSERT INTO billing.checkout_attempts(id,user_id,license_id,tier,billing_interval,status,stripe_parameters,expires_at) VALUES($1,$2,$3,'pro','month','creating','{}',now()+interval '35 minutes')", [randomUUID(), f.userId, f.licenseId]);
  await f.ready(); await f.worker.runOnce(); expect((await f.state()).last_error_code).toBe("closure_recovery_required");
  await admin.query("UPDATE billing.checkout_attempts SET status='failed' WHERE user_id=$1", [f.userId]); await f.ready(); await f.drain(); expect(f.gateway.deleteCustomer).not.toHaveBeenCalled();
});
it("blocks foreign session metadata and enabled checkout-recovery links before expiration", async () => {
  const f = await fixture(), session = await f.session(); session.metadata.user_id = "foreign-user"; await f.begin(); await f.worker.runOnce();
  expect((await f.state()).last_error_code).toBe("closure_identity_mismatch"); expect(f.gateway.expireSession).not.toHaveBeenCalled();
  session.metadata.user_id = f.userId; f.sessions.get(session.id).after_expiration = { recovery: { enabled: true } }; await f.ready(); await f.worker.runOnce();
  expect((await f.state()).last_error_code).toBe("closure_recovery_link_enabled"); expect(f.gateway.expireSession).not.toHaveBeenCalled();
});
it("refuses to delete a customer also attributed to another account", async () => {
  const f = await fixture(), other = await fixture(); await other.session("purchase", f.customerId, "complete"); await f.begin(); await f.worker.runOnce();
  expect((await f.state()).last_error_code).toBe("closure_identity_mismatch"); expect(f.gateway.deleteCustomer).not.toHaveBeenCalled(); expect(f.gateway.retrieveCustomer).not.toHaveBeenCalled();
});
it("persists customer pagination while independently processing newly discovered resources", async () => {
  const f = await fixture(); const a = await f.session("discovered"), b = await f.session("discovered"); const ids = [a.id,b.id].sort();
  f.gateway.listSessions.mockImplementation(async query => ({ data: [structuredClone(f.sessions.get(query.starting_after ? ids[1]! : ids[0]!))], has_more: !query.starting_after }));
  await f.begin(); await f.worker.runOnce();
  expect((await admin.query("SELECT cursor FROM billing.account_closure_resources WHERE user_id=$1 AND kind='customer'", [f.userId])).rows[0].cursor).toBe(ids[0]);
  await f.drain(); expect(f.gateway.listSessions).toHaveBeenCalledTimes(2); expect(f.gateway.listSessions.mock.calls[1]![0].starting_after).toBe(ids[0]); expect(f.gateway.expireSession).toHaveBeenCalledTimes(2);
});
it("does not let concurrent workers process the same locked account", async () => {
  const f = await fixture(); await f.session(); await f.begin(); let release!: () => void, entered!: () => void;
  const gate = new Promise<void>(done => { release = done; }), ready = new Promise<void>(done => { entered = done; }); const original = f.gateway.expireSession.getMockImplementation()!;
  f.gateway.expireSession.mockImplementationOnce(async id => { entered(); await gate; return original(id); });
  const first = f.worker.runOnce(); await ready;
  try { expect(await f.worker.runOnce()).toBe(false); } finally { release(); }
  expect(await first).toBe(true); await f.drain(); expect(f.gateway.expireSession).toHaveBeenCalledTimes(1);
});
it("discards a lost database connection and resumes from the externally completed operation", async () => {
  const f = await fixture(); await f.session(); await f.begin(); let release!: () => void, entered!: () => void;
  const gate = new Promise<void>(done => { release = done; }), ready = new Promise<void>(done => { entered = done; }); const original = f.gateway.expireSession.getMockImplementation()!;
  f.gateway.expireSession.mockImplementationOnce(async id => { entered(); await gate; return original(id); });
  // Attach rejection handling before terminating the connection; the provider
  // operation can complete after PostgreSQL has already released its locks.
  const pending = f.worker.runOnce().then(() => null, error => error); await ready;
  try {
    const pid = (await admin.query("SELECT pid FROM pg_stat_activity WHERE application_name='misty-closure-worker-test' AND state='idle in transaction'")).rows[0]?.pid;
    expect(pid).toBeTruthy(); await admin.query("SELECT pg_terminate_backend($1)", [pid]);
  } finally { release(); }
  expect(await pending).toBeInstanceOf(Error); expect((await f.state()).state).toBe("closing");
  await f.drain(); expect(f.gateway.expireSession).toHaveBeenCalledTimes(1);
});
it("learns the customer and subscription from a completed checkout when no customer was stored yet", async () => {
  const f = await fixture(false), customer = f.addCustomer(); const sub = await f.subscription("active", customer, false); const session = await f.session("native", customer, "complete");
  f.sessions.get(session.id).subscription = sub.id; await f.begin(); await f.drain(); expect(sub.status).toBe("canceled"); expect(f.customers.get(customer)?.deleted).toBe(true);
});
it("rejects invalid pagination and false customer-deletion acknowledgements", async () => {
  const f = await fixture(); await f.begin(); f.gateway.listSessions.mockResolvedValueOnce({ data: [], has_more: true }); await f.worker.runOnce();
  expect((await f.state()).last_error_code).toBe("closure_pagination_invalid"); expect(f.gateway.deleteCustomer).not.toHaveBeenCalled();
  await f.ready(); await f.worker.runOnce(); await f.worker.runOnce();
  f.gateway.deleteCustomer.mockResolvedValueOnce({ id: "cus_foreign", deleted: true }); await f.worker.runOnce();
  expect((await f.state()).last_error_code).toBe("closure_provider_unavailable"); expect((await f.state()).state).toBe("closing");
  await f.ready(); await f.drain();
});
