import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Hono } from "hono";
import { generateKeyPair } from "jose";
import { Pool } from "pg";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { subscriptionEntitlementEventSchema } from "../../../../../packages/service-contracts/src/payments.js";
import { createEntitlementRepository } from "../../../../api/src/modules/entitlements/repository.js";
import { createEntitlementRoutes } from "../../../../api/src/modules/entitlements/routes.js";
import { createEntitlementEffects } from "../../../../api/src/modules/entitlements/service.js";
import { importLegacyPurchases } from "../legacy-purchases/import.js";
import { importLegacySubscriptions } from "../subscriptions/import.js";
import { createPriceCatalog } from "../subscriptions/model.js";
import { lockBillingAccount, synchronizeSubscription } from "../subscriptions/repository.js";
import { initializeSubscriptionEntitlements } from "./initialize.js";
import { createEntitlementDispatcher } from "./dispatcher.js";
import { createEntitlementOutbox, enqueueEntitlement, enqueuePurchaseReversal } from "./repository.js";

const admin = createTestDatabase(), users: string[] = [], subscriptions: string[] = [];
const end = new Date(Date.now() + 30 * 86400000); end.setMilliseconds(0);
let payments: Pool, application: Pool;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../migrations/", import.meta.url))), "billing");
  await admin.query(`DO $$ BEGIN
    IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_initialize_payments_test') THEN CREATE ROLE misty_initialize_payments_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF;
    IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_initialize_api_test') THEN CREATE ROLE misty_initialize_api_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF;
    END $$;
    GRANT USAGE ON SCHEMA billing TO misty_initialize_payments_test; GRANT SELECT ON billing.account_closures TO misty_initialize_payments_test;
    GRANT SELECT,INSERT,UPDATE ON billing.accounts,billing.subscriptions,billing.entitlement_versions,billing.entitlement_outbox TO misty_initialize_payments_test;
    GRANT USAGE ON SCHEMA public TO misty_initialize_api_test;
    GRANT SELECT,UPDATE ON users,licenses TO misty_initialize_api_test;
    GRANT SELECT,INSERT,UPDATE ON payment_entitlement_inbox,payment_entitlement_projections,payment_purchase_reversals,
      hosted_ai_wallets,hosted_ai_reservations,hosted_ai_usage_ledger TO misty_initialize_api_test;
    GRANT SELECT,UPDATE ON license_lifetime_grants TO misty_initialize_api_test;`);
  payments = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_initialize_payments_test", max: 4 });
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_initialize_api_test", max: 2 });
}, 60000);
afterEach(async () => {
  await admin.query("DELETE FROM billing.entitlement_outbox WHERE user_id=ANY($1::text[])", [users]);
  await admin.query("DELETE FROM billing.entitlement_versions WHERE user_id=ANY($1::text[])", [users]);
  await admin.query("DELETE FROM billing.legacy_subscription_records WHERE stripe_subscription_id=ANY($1::text[])", [subscriptions]);
  await admin.query("DELETE FROM billing.subscriptions WHERE user_id=ANY($1::text[])", [users]);
  await admin.query("DELETE FROM billing.legacy_purchases WHERE user_id=ANY($1::text[])", [users]);
  await admin.query("DELETE FROM billing.accounts WHERE user_id=ANY($1::text[])", [users]);
  await admin.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
  await admin.query("DELETE FROM payment_entitlement_inbox WHERE user_id=ANY($1::text[])", [users]);
  await admin.query("DELETE FROM billing.cutover_checkpoints WHERE name IN ('legacy_purchases_imported','legacy_subscriptions_imported')");
  users.length = 0; subscriptions.length = 0;
});
afterAll(async () => { if (payments) await payments.end(); if (application) await application.end(); await admin.end(); });
async function fixture(status = "active", legacyTier: string | null = null) {
  const userId = `init_${randomUUID().replaceAll("-", "").slice(0, 12)}`, licenseId = `license_${userId}`, subscriptionId = `sub_${randomUUID()}`;
  users.push(userId);
  await withTransaction(admin, async (tx) => {
    await tx.query("INSERT INTO users(id,email,password_hash,username,license_id) VALUES($1,$2,'test-only',$1,$3)", [userId, `${userId}@example.invalid`, licenseId]);
    await tx.query(`INSERT INTO licenses(id,user_id,tier,status,expires_at,legacy_tier,trial_started_at)
      VALUES($1,$2,$3,$4,$5,$6,now()-INTERVAL '1 day')`, [licenseId, userId, status === "canceled" ? legacyTier ?? "basic" : "pro",
      status === "trialing" || status === "local-trial" ? "trialing" : "active", status === "trialing" || status === "local-trial" ? end : null, legacyTier]);
    if (status === "local-trial") {
      await tx.query("INSERT INTO billing.accounts(user_id,license_id) VALUES($1,$2)", [userId, licenseId]);
    } else {
      subscriptions.push(subscriptionId);
      await tx.query(`INSERT INTO stripe_subscriptions(id,user_id,license_id,stripe_subscription_id,stripe_customer_id,stripe_price_id,tier,billing_interval,status,current_period_end)
        VALUES($1,$2,$3,$4,$5,'price_init','pro','month',$6,$7)`, [randomUUID(), userId, licenseId, subscriptionId, `cus_${userId}`, status, end]);
    }
  }, { mode: "service" });
  return { userId, licenseId, subscriptionId,
    license: async () => (await admin.query("SELECT tier,status,expires_at,legacy_tier,trial_started_at FROM licenses WHERE id=$1", [licenseId])).rows[0],
    marker: async () => (await admin.query("SELECT subscription_snapshot_enqueued FROM billing.accounts WHERE user_id=$1", [userId])).rows[0].subscription_snapshot_enqueued,
    event: async () => subscriptionEntitlementEventSchema.parse((await admin.query("SELECT payload FROM billing.entitlement_outbox WHERE user_id=$1 AND payload ? 'subscription' ORDER BY revision DESC LIMIT 1", [userId])).rows[0].payload),
  };
}
async function imports() { await importLegacyPurchases(admin, { commit: true }); await importLegacySubscriptions(admin, { commit: true }); }
const repository = () => createEntitlementRepository({ pool: application, ...createEntitlementEffects() });

it("requires verified imports and migration credentials, and dry-runs without projecting or queuing", async () => {
  const f = await fixture();
  await expect(initializeSubscriptionEntitlements(admin, { commit: false })).rejects.toThrow("Verify the legacy subscription import first");
  await imports();
  expect(await initializeSubscriptionEntitlements(admin, { commit: false })).toMatchObject({ committed: false, candidates: "1", enqueued: "0", conflicts: "0" });
  expect(await f.marker()).toBe(false);
  expect((await admin.query("SELECT 1 FROM payment_entitlement_projections WHERE user_id=$1", [f.userId])).rowCount).toBe(0);
  await expect(initializeSubscriptionEntitlements(payments, { commit: true })).rejects.toThrow("permission denied");
  await expect(payments.query("SELECT * FROM public.licenses")).rejects.toThrow("permission denied");
  await expect(application.query("SELECT * FROM billing.accounts")).rejects.toThrow("permission denied");
});

it("delivers imported access through signed HTTP exactly once while retaining trial history, spent usage and a live reservation", async () => {
  const f = await fixture("trialing"), license = await f.license(), reservation = randomUUID();
  await admin.query(`INSERT INTO hosted_ai_wallets(user_id,weekly_allowance_microusd,weekly_remaining_microusd,weekly_consumed_microusd,reserved_microusd,reset_at)
    VALUES($1,900000,710000,0,25000,$2)`, [f.userId, end]);
  await admin.query(`INSERT INTO hosted_ai_reservations(id,user_id,idempotency_key,meter,reserved_microusd,lease_expires_at)
    VALUES($1,$2,$1,'assistant_ai',25000,$3)`, [reservation, f.userId, end]);
  await imports();
  expect(await initializeSubscriptionEntitlements(admin, { commit: true })).toMatchObject({ enqueued: "1", awaitingAcknowledgement: "1", awaitingProjection: "1" });
  expect(await initializeSubscriptionEntitlements(admin, { commit: true })).toMatchObject({ enqueued: "0", alreadyEnqueued: "1" });
  expect(await f.license()).toEqual(license);
  const keys = await generateKeyPair("EdDSA", { crv: "Ed25519" });
  const api = new Hono().route("/internal/payments/entitlements", createEntitlementRoutes({ publicKeys: new Map([["init-key", keys.publicKey]]), repository: repository() }));
  let loseAcknowledgement = true;
  const dispatcher = createEntitlementDispatcher({ endpoint: "https://api.example.invalid/internal/payments/entitlements",
    keyId: "init-key", privateKey: keys.privateKey, outbox: createEntitlementOutbox(payments), fetch: async (input, init) => {
      const response = await api.request(new Request(String(input), init));
      expect(response.status).toBe(200);
      if (loseAcknowledgement) { loseAcknowledgement = false; throw new Error("lost response after commit"); }
      return response;
    } });
  expect(await dispatcher.runOnce()).toBe(true);
  expect(await initializeSubscriptionEntitlements(admin, { commit: false })).toMatchObject({ awaitingAcknowledgement: "1", awaitingProjection: "0" });
  await admin.query("UPDATE billing.entitlement_outbox SET available_at=now() WHERE user_id=$1", [f.userId]);
  expect(await dispatcher.runOnce()).toBe(true);
  expect(await initializeSubscriptionEntitlements(admin, { commit: false })).toMatchObject({ awaitingAcknowledgement: "0", awaitingProjection: "0", missingDeliveryEvidence: "0" });
  expect(await f.license()).toEqual(license);
  expect((await admin.query(`SELECT weekly_remaining_microusd,weekly_consumed_microusd,reserved_microusd,reset_at FROM hosted_ai_wallets WHERE user_id=$1`, [f.userId])).rows[0])
    .toEqual({ weekly_remaining_microusd: "710000", weekly_consumed_microusd: "190000", reserved_microusd: "25000", reset_at: end });
  expect((await admin.query("SELECT status FROM hosted_ai_reservations WHERE id=$1", [reservation])).rows[0].status).toBe("reserved");
  expect((await admin.query("SELECT 1 FROM hosted_ai_usage_ledger WHERE user_id=$1", [f.userId])).rowCount).toBe(0);
});

it("preserves lifetime fallback, leaves local trials without subscription history alone, and defers inactive accounts", async () => {
  const lifetime = await fixture("canceled", "max"), local = await fixture("local-trial"), inactive = await fixture();
  const localLicense = await local.license();
  await admin.query("UPDATE users SET lifecycle_state='pending_deletion' WHERE id=$1", [inactive.userId]);
  await imports();
  expect(await initializeSubscriptionEntitlements(admin, { commit: true })).toMatchObject({ enqueued: "1", deferredInactive: "1", conflicts: "0" });
  await repository().receive(await lifetime.event());
  expect(await lifetime.license()).toMatchObject({ tier: "max", legacy_tier: "max", status: "active" });
  expect(await local.license()).toEqual(localLicense);
  expect(await local.marker()).toBe(false);
  expect(await inactive.marker()).toBe(false);
  await admin.query("UPDATE users SET lifecycle_state='active' WHERE id=$1", [inactive.userId]);
  expect(await initializeSubscriptionEntitlements(admin, { commit: true })).toMatchObject({ enqueued: "1", alreadyEnqueued: "1", deferredInactive: "0" });
});

it("rejects license mismatch and changed legacy history without committing a partial batch", async () => {
  const first = await fixture(), conflict = await fixture("canceled"); await imports();
  await admin.query("UPDATE licenses SET tier='pro',status='trialing',expires_at=$2 WHERE id=$1", [conflict.licenseId, end]);
  expect(await initializeSubscriptionEntitlements(admin, { commit: false })).toMatchObject({ candidates: "1", conflicts: "1" });
  await expect(initializeSubscriptionEntitlements(admin, { commit: true })).rejects.toThrow("no initial snapshots were committed");
  expect(await first.marker()).toBe(false); expect(await conflict.marker()).toBe(false);
  expect((await admin.query("SELECT 1 FROM billing.entitlement_versions WHERE user_id=ANY($1::text[])", [users])).rowCount).toBe(0);
  await admin.query("UPDATE stripe_subscriptions SET cancel_at_period_end=true WHERE stripe_subscription_id=$1", [first.subscriptionId]);
  await expect(initializeSubscriptionEntitlements(admin, { commit: true })).rejects.toThrow("differs from the source");
});

it("rolls back the event, revision and marker if persistence fails, then retries and rejects an older initial snapshot", async () => {
  const f = await fixture(); await imports();
  await admin.query(`CREATE FUNCTION billing.test_reject_snapshot_marker() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic marker failure'; END $$;
    CREATE TRIGGER test_reject_snapshot_marker BEFORE UPDATE OF subscription_snapshot_enqueued ON billing.accounts FOR EACH ROW EXECUTE FUNCTION billing.test_reject_snapshot_marker()`);
  try { await expect(initializeSubscriptionEntitlements(admin, { commit: true })).rejects.toThrow("synthetic marker failure"); }
  finally { await admin.query("DROP TRIGGER test_reject_snapshot_marker ON billing.accounts; DROP FUNCTION billing.test_reject_snapshot_marker()"); }
  expect(await f.marker()).toBe(false);
  expect((await admin.query("SELECT 1 FROM billing.entitlement_outbox WHERE user_id=$1", [f.userId])).rowCount).toBe(0);
  expect((await admin.query("SELECT 1 FROM billing.entitlement_versions WHERE user_id=$1", [f.userId])).rowCount).toBe(0);
  await initializeSubscriptionEntitlements(admin, { commit: true });
  const initial = await f.event();
  const newer = await withTransaction(payments, async (tx) => {
    await lockBillingAccount(tx, f.userId);
    return enqueueEntitlement(tx, { userId: f.userId, licenseId: f.licenseId, subscription: { ...initial.subscription!, status: "canceled" } });
  });
  const consumer = repository();
  expect(await consumer.receive(newer)).toBe("applied");
  expect(await consumer.receive(initial)).toBe("superseded");
  expect(await f.license()).toMatchObject({ tier: "basic" });
});

it("initializes unchanged canonical state once even when the account already has a purchase-reversal revision", async () => {
  const f = await fixture(); await imports();
  await withTransaction(payments, async (tx) => {
    await lockBillingAccount(tx, f.userId);
    await enqueuePurchaseReversal(tx, { userId: f.userId, licenseId: f.licenseId, purchaseId: randomUUID(), reason: "refunded" });
  });
  expect(await f.marker()).toBe(false);
  let inFlight = 0, maximum = 0;
  const synchronize = () => withTransaction(payments, async (tx) => synchronizeSubscription(tx, {
    account: await lockBillingAccount(tx, f.userId), subscriptionId: f.subscriptionId,
    catalog: createPriceCatalog([{ id: "price_init", tier: "pro", interval: "month" }]),
    fetchSubscription: async () => {
      maximum = Math.max(maximum, ++inFlight); await new Promise((resolve) => setTimeout(resolve, 15)); inFlight--;
      return { id: f.subscriptionId, customer: `cus_${f.userId}`, metadata: { user_id: f.userId, license_id: f.licenseId, kind: "subscription", tier: "pro", interval: "month" },
        status: "active", cancel_at_period_end: false, items: { data: [{ current_period_end: end.getTime() / 1000, price: { id: "price_init", recurring: { interval: "month" } } }] } };
    },
  }));
  expect(await Promise.all([synchronize(), synchronize()])).toEqual([false, false]);
  expect(maximum).toBe(1); expect(await f.marker()).toBe(true);
  expect((await f.event()).revision).toBe("2");
  expect((await admin.query("SELECT count(*)::text count FROM billing.entitlement_outbox WHERE user_id=$1", [f.userId])).rows[0].count).toBe("2");
  // A future delivered-event retention policy must not reset initialization.
  await admin.query("DELETE FROM billing.entitlement_outbox WHERE user_id=$1", [f.userId]);
  expect(await synchronize()).toBe(false);
  expect((await admin.query("SELECT 1 FROM billing.entitlement_outbox WHERE user_id=$1", [f.userId])).rowCount).toBe(0);
});

it("continues across bounded pages without skipping accounts as their markers change", async () => {
  for (let batch = 0; batch < 26; batch++) await Promise.all(Array.from({ length: batch === 25 ? 1 : 4 }, () => fixture()));
  await imports();
  expect(await initializeSubscriptionEntitlements(admin, { commit: false })).toMatchObject({ candidates: "101", enqueued: "0" });
  expect(await initializeSubscriptionEntitlements(admin, { commit: true })).toMatchObject({ candidates: "101", enqueued: "101", conflicts: "0", awaitingProjection: "101" });
  expect(await initializeSubscriptionEntitlements(admin, { commit: true })).toMatchObject({ candidates: "0", enqueued: "0", alreadyEnqueued: "101" });
});
