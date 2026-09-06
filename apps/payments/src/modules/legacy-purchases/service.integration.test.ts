import { createHash, randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { afterAll, beforeAll, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { createPriceCatalog } from "../subscriptions/model.js";
import { createWebhookRepository } from "../webhooks/repository.js";
import { createWebhookWorker } from "../webhooks/worker.js";
import { importLegacyPurchases } from "./import.js";

const admin = createTestDatabase();
const userId = `legacy_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
const licenseId = `license_${userId}`;
const purchaseId = randomUUID();
const paymentIntentId = `pi_${purchaseId}`;
const chargeId = `ch_${purchaseId}`;
const events: string[] = [];
let payments: Pool;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../migrations/", import.meta.url))), "billing");
  await admin.query(`DO $$ BEGIN
    IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_hono_payments_test') THEN
      CREATE ROLE misty_hono_payments_test NOLOGIN NOSUPERUSER NOBYPASSRLS;
    END IF;
  END $$;
  GRANT USAGE ON SCHEMA billing TO misty_hono_payments_test; GRANT SELECT ON billing.account_closures TO misty_hono_payments_test;
  GRANT SELECT,INSERT,UPDATE ON billing.accounts,billing.webhook_inbox,billing.entitlement_versions,billing.entitlement_outbox TO misty_hono_payments_test;
  GRANT SELECT,UPDATE ON billing.legacy_purchases TO misty_hono_payments_test;
  GRANT SELECT ON billing.cutover_checkpoints TO misty_hono_payments_test;`);
  await withTransaction(admin, async (tx) => {
    await tx.query("INSERT INTO users(id,email,password_hash,username,license_id) VALUES($1,$2,'test-only',$1,$3)", [userId, `${userId}@example.invalid`, licenseId]);
    await tx.query("INSERT INTO licenses(id,user_id,legacy_tier) VALUES($1,$2,'max')", [licenseId, userId]);
    await tx.query(`INSERT INTO stripe_purchases(id,user_id,license_id,tier_purchased,stripe_checkout_session_id,stripe_payment_intent_id,amount)
      VALUES($1,$2,$3,'pro',$4,$5,12345)`, [purchaseId, userId, licenseId, `cs_${purchaseId}`, paymentIntentId]);
  });
  payments = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_payments_test", max: 4 });
}, 60000);
afterAll(async () => {
  if (payments) await payments.end();
  await admin.query("DELETE FROM billing.cutover_checkpoints WHERE name='legacy_purchases_imported'");
  await admin.query("DELETE FROM billing.webhook_inbox WHERE event_id=ANY($1::text[])", [events]);
  await admin.query("DELETE FROM billing.entitlement_outbox WHERE user_id=$1", [userId]);
  await admin.query("DELETE FROM billing.entitlement_versions WHERE user_id=$1", [userId]);
  await admin.query("DELETE FROM billing.legacy_purchases WHERE user_id=$1", [userId]);
  await admin.query("DELETE FROM billing.accounts WHERE user_id=$1", [userId]);
  await admin.query("DELETE FROM users WHERE id=$1", [userId]);
  await admin.end();
});
async function enqueue(type: string, object: unknown) {
  const eventId = `evt_${randomUUID()}`;
  events.push(eventId);
  const payload = { id: eventId, type, data: { object } };
  await createWebhookRepository(payments).accept({ id: eventId, type, created: 1, payload,
    payloadSha256: createHash("sha256").update(JSON.stringify(payload)).digest("hex") });
  return eventId;
}
function worker(fetchCharge?: (id: string) => Promise<unknown>) {
  return createWebhookWorker({ inbox: createWebhookRepository(payments),
    catalog: createPriceCatalog([{ id: "price_test", tier: "pro", interval: "month" }]),
    fetchSubscription: async () => { throw new Error("Unexpected subscription lookup"); },
    ...(fetchCharge ? { fetchCharge } : {}),
  });
}

it("keeps refunds retryable until historical purchases are imported", async () => {
  const eventId = await enqueue("charge.refunded", { id: chargeId, payment_intent: paymentIntentId });
  await worker().runOnce();
  expect((await admin.query("SELECT state,last_error_code FROM billing.webhook_inbox WHERE event_id=$1", [eventId])).rows[0])
    .toEqual({ state: "failed", last_error_code: "legacy_import_pending" });
  await expect(payments.query("INSERT INTO billing.cutover_checkpoints(name,evidence) VALUES('forged','{}')")).rejects.toThrow("permission denied");
});

it("imports exact history only on commit and refuses conflicting target identities", async () => {
  expect(await importLegacyPurchases(admin, { commit: false })).toMatchObject({ committed: false, previouslyImported: false });
  expect((await admin.query("SELECT 1 FROM billing.legacy_purchases WHERE id=$1", [purchaseId])).rowCount).toBe(0);
  await admin.query("INSERT INTO billing.accounts(user_id,license_id) VALUES($1,'conflicting-license')", [userId]);
  await expect(importLegacyPurchases(admin, { commit: true })).rejects.toThrow("account license conflicts");
  expect((await admin.query("SELECT 1 FROM billing.cutover_checkpoints WHERE name='legacy_purchases_imported'")).rowCount).toBe(0);
  await admin.query("DELETE FROM billing.accounts WHERE user_id=$1", [userId]);
  expect(await importLegacyPurchases(admin, { commit: true })).toMatchObject({ committed: true, previouslyImported: false });
  expect(await importLegacyPurchases(admin, { commit: true })).toMatchObject({ previouslyImported: true });
  expect((await admin.query("SELECT to_jsonb(p)=to_jsonb(b) same FROM stripe_purchases p JOIN billing.legacy_purchases b USING(id) WHERE p.id=$1", [purchaseId])).rows[0].same).toBe(true);
  expect((await admin.query("SELECT legacy_tier FROM licenses WHERE id=$1", [licenseId])).rows[0].legacy_tier).toBe("max");
});

it("recovers the pending refund by payment intent and emits just one reversal across duplicate dispute events", async () => {
  await admin.query("UPDATE billing.webhook_inbox SET available_at=now()-INTERVAL '1 second' WHERE event_id=$1", [events[0]]);
  await worker().runOnce();
  expect((await admin.query("SELECT status,stripe_charge_id FROM billing.legacy_purchases WHERE id=$1", [purchaseId])).rows[0])
    .toEqual({ status: "refunded", stripe_charge_id: chargeId });
  await enqueue("charge.dispute.created", { charge: chargeId });
  await worker(async () => ({ id: chargeId, payment_intent: paymentIntentId })).runOnce();
  const outbox = await admin.query("SELECT payload FROM billing.entitlement_outbox WHERE user_id=$1", [userId]);
  expect(outbox.rowCount).toBe(1);
  expect(outbox.rows[0].payload).toMatchObject({ kind: "purchase_reversal", purchaseId, reason: "refunded", userId, licenseId });
  await expect(importLegacyPurchases(admin, { commit: true })).rejects.toThrow("differs from the source");
});

it("ignores an unrelated charge only after import and retries an unavailable dispute lookup", async () => {
  const ignored = await enqueue("charge.refunded", { id: "ch_unrelated" });
  await worker().runOnce();
  expect((await admin.query("SELECT state FROM billing.webhook_inbox WHERE event_id=$1", [ignored])).rows[0].state).toBe("completed");
  const failed = await enqueue("charge.dispute.created", { charge: "ch_unavailable" });
  await worker(async () => { throw new Error("Stripe unavailable"); }).runOnce();
  expect((await admin.query("SELECT state FROM billing.webhook_inbox WHERE event_id=$1", [failed])).rows[0].state).toBe("failed");
  expect((await admin.query("SELECT count(*) FROM billing.entitlement_outbox WHERE user_id=$1", [userId])).rows[0].count).toBe("1");
});
