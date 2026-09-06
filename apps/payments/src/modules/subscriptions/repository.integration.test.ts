import { createHash, randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { afterAll, beforeAll, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { createWebhookRepository } from "../webhooks/repository.js";
import { createWebhookWorker } from "../webhooks/worker.js";
import { createPriceCatalog } from "./model.js";
import { createSubscriptionReconciler } from "./reconciler.js";

const admin = createTestDatabase();
const userId = `subtest_${randomUUID()}`;
const licenseId = `license_${userId}`;
const subscriptionId = `sub_${randomUUID()}`;
const events: string[] = [];
const catalog = createPriceCatalog([{ id: "price_test", tier: "pro", interval: "month" }]);
const canonical = {
  id: subscriptionId, customer: `cus_${userId}`, metadata: { user_id: userId, license_id: licenseId, kind: "subscription", tier: "pro", interval: "month" },
  status: "active", cancel_at_period_end: false,
  items: { data: [{ current_period_end: 1900000000, price: { id: "price_test", recurring: { interval: "month" } } }] },
};
let payments: Pool;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../migrations/", import.meta.url))), "billing");
  await admin.query(`DO $$ BEGIN
    IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_hono_payments_test') THEN
      CREATE ROLE misty_hono_payments_test NOLOGIN NOSUPERUSER NOBYPASSRLS;
    END IF;
  END $$;
  GRANT USAGE ON SCHEMA billing TO misty_hono_payments_test; GRANT SELECT ON billing.account_closures TO misty_hono_payments_test;
  GRANT SELECT,INSERT,UPDATE ON billing.accounts,billing.subscriptions,billing.webhook_inbox,
    billing.entitlement_versions,billing.entitlement_outbox,billing.checkout_attempts TO misty_hono_payments_test;`);
  await admin.query("INSERT INTO billing.accounts(user_id,license_id) VALUES($1,$2)", [userId, licenseId]);
  payments = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_payments_test", max: 4 });
});
afterAll(async () => {
  if (payments) await payments.end();
  await admin.query("DELETE FROM billing.webhook_inbox WHERE event_id=ANY($1::text[])", [events]);
  await admin.query("DELETE FROM billing.entitlement_outbox WHERE user_id=$1", [userId]);
  await admin.query("DELETE FROM billing.entitlement_versions WHERE user_id=$1", [userId]);
  await admin.query("DELETE FROM billing.subscriptions WHERE user_id=$1", [userId]);
  await admin.query("DELETE FROM billing.accounts WHERE user_id=$1", [userId]);
  await admin.end();
});
async function enqueue(status: string) {
  const eventId = `evt_${randomUUID()}`;
  events.push(eventId);
  const payload = { id: eventId, type: "customer.subscription.updated", data: { object: { ...canonical, status } } };
  await createWebhookRepository(payments).accept({ id: eventId, type: payload.type, created: 1, payload,
    payloadSha256: createHash("sha256").update(JSON.stringify(payload)).digest("hex") });
  return eventId;
}
const state = async () => (await admin.query("SELECT status FROM billing.subscriptions WHERE stripe_subscription_id=$1", [subscriptionId])).rows[0].status;
const revision = async () => (await admin.query("SELECT revision FROM billing.entitlement_versions WHERE user_id=$1", [userId])).rows[0].revision;

it("uses canonical state and cannot restore canceled access from a late activation webhook", async () => {
  const worker = createWebhookWorker({ inbox: createWebhookRepository(payments), catalog, fetchSubscription: async () => structuredClone(canonical) });
  await enqueue("active");
  await worker.runOnce();
  expect(await state()).toBe("active");
  expect(await revision()).toBe("1");
  canonical.status = "canceled";
  await enqueue("canceled");
  await worker.runOnce();
  expect(await revision()).toBe("2");
  await enqueue("active");
  await worker.runOnce();
  expect(await state()).toBe("canceled");
  expect(await revision()).toBe("2");
});

it("serializes canonical reads across workers for the same account", async () => {
  let inFlight = 0;
  let maxInFlight = 0;
  const worker = createWebhookWorker({ inbox: createWebhookRepository(payments), catalog, fetchSubscription: async () => {
    inFlight++;
    maxInFlight = Math.max(maxInFlight, inFlight);
    await new Promise((resolve) => setTimeout(resolve, 25));
    inFlight--;
    return structuredClone(canonical);
  } });
  await enqueue("active");
  await enqueue("canceled");
  await Promise.all([worker.runOnce(), worker.runOnce()]);
  expect(maxInFlight).toBe(1);
  expect(await state()).toBe("canceled");
});

it("keeps malformed or mismatched subscription data retryable without granting access", async () => {
  const eventId = await enqueue("active");
  const worker = createWebhookWorker({ inbox: createWebhookRepository(payments), catalog,
    fetchSubscription: async () => ({ ...canonical, status: "active", metadata: { ...canonical.metadata, license_id: "wrong-license" } }),
  });
  await worker.runOnce();
  expect(await state()).toBe("canceled");
  expect(await revision()).toBe("2");
  expect((await admin.query("SELECT state,last_error_code FROM billing.webhook_inbox WHERE event_id=$1", [eventId])).rows[0])
    .toEqual({ state: "failed", last_error_code: "subscription_identity_mismatch" });
});

it("reconciles missed events and backs off Stripe outages without partial state changes", async () => {
  await admin.query("UPDATE billing.subscriptions SET status='active',reconcile_after=now()-INTERVAL '1 second' WHERE stripe_subscription_id=$1", [subscriptionId]);
  const failed = createSubscriptionReconciler({ pool: payments, catalog, fetchSubscription: async () => { throw new Error("Test Stripe outage"); } });
  expect(await failed.runOnce()).toBe(true);
  expect(await failed.runOnce()).toBe(false);
  expect((await admin.query("SELECT reconcile_failures,last_reconcile_error FROM billing.subscriptions WHERE stripe_subscription_id=$1", [subscriptionId])).rows[0])
    .toEqual({ reconcile_failures: 1, last_reconcile_error: "stripe_unavailable" });
  await admin.query("UPDATE billing.subscriptions SET reconcile_after=now()-INTERVAL '1 second' WHERE stripe_subscription_id=$1", [subscriptionId]);
  const recovered = createSubscriptionReconciler({ pool: payments, catalog, fetchSubscription: async () => structuredClone(canonical) });
  expect(await recovered.runOnce()).toBe(true);
  expect(await state()).toBe("canceled");
  expect((await admin.query("SELECT reconcile_failures FROM billing.subscriptions WHERE stripe_subscription_id=$1", [subscriptionId])).rows[0].reconcile_failures).toBe(0);
});
