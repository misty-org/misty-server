import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { importLegacyPurchases } from "../legacy-purchases/import.js";
import { createBillingSummaryRepository } from "../accounts/summary.js";
import { importLegacySubscriptions } from "./import.js";

const admin = createTestDatabase(), users: string[] = [], subscriptions: string[] = [];
let restricted: Pool;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../migrations/", import.meta.url))), "billing");
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_subscription_import_test') THEN CREATE ROLE misty_subscription_import_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA billing TO misty_subscription_import_test; GRANT SELECT ON billing.account_closures TO misty_subscription_import_test;
    GRANT SELECT ON billing.accounts,billing.subscriptions,billing.legacy_purchases,billing.cutover_checkpoints TO misty_subscription_import_test;`);
  restricted = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_subscription_import_test", max: 2 });
}, 60000);
afterEach(async () => {
  await admin.query("DELETE FROM billing.legacy_subscription_records WHERE stripe_subscription_id=ANY($1::text[])", [subscriptions]); subscriptions.length = 0;
  await admin.query("DELETE FROM billing.subscriptions WHERE user_id=ANY($1::text[])", [users]);
  await admin.query("DELETE FROM billing.legacy_purchases WHERE user_id=ANY($1::text[])", [users]);
  await admin.query("DELETE FROM billing.accounts WHERE user_id=ANY($1::text[])", [users]);
  await admin.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]); users.length = 0;
  await admin.query("DELETE FROM billing.cutover_checkpoints WHERE name IN ('legacy_purchases_imported','legacy_subscriptions_imported')");
});
afterAll(async () => { if (restricted) await restricted.end(); await admin.end(); });
async function fixture() {
  const id = `subimport_${randomUUID().replaceAll("-", "").slice(0, 12)}`, license = `license_${id}`, customer = `cus_${id}`; users.push(id);
  await withTransaction(admin, async (tx) => {
    await tx.query("INSERT INTO users(id,email,password_hash,username,license_id) VALUES($1,$2,'test-only',$1,$3)", [id, `${id}@example.invalid`, license]);
    await tx.query("INSERT INTO licenses(id,user_id,legacy_tier) VALUES($1,$2,'max')", [license, id]);
  }, { mode: "service" });
  const add = async (status = "active", selectedCustomer = customer, updated = "2026-08-01T00:00:00Z") => {
    const sub = `sub_${randomUUID()}`; subscriptions.push(sub);
    await admin.query(`INSERT INTO stripe_subscriptions(id,user_id,license_id,stripe_subscription_id,stripe_customer_id,stripe_price_id,tier,billing_interval,status,
      current_period_end,cancel_at_period_end,updated_at,source_event_id,source_event_created_at)
      VALUES($1,$2,$3,$4,$5,'price_import','pro','month',$6,'2026-10-01',true,$7,'evt_import','2026-08-01T00:00:00Z')`, [randomUUID(), id, license, sub, selectedCustomer, status, updated]);
    return sub;
  };
  return { id, license, customer, add };
}
it("requires a verified unchanged purchase import and cannot be run with runtime credentials", async () => {
  await fixture();
  await expect(importLegacySubscriptions(admin, { commit: false })).rejects.toThrow("Verify the legacy purchase import first");
  await expect(importLegacySubscriptions(restricted, { commit: true })).rejects.toThrow("permission denied");
  await expect(restricted.query("INSERT INTO billing.cutover_checkpoints(name,evidence) VALUES('forged','{}')")).rejects.toThrow("permission denied");
});
it("dry-runs without mutation, copies exact subscriptions and customer preference, and makes history readable", async () => {
  const f = await fixture(), active = await f.add(); await f.add("canceled", "cus_old_customer", "2026-08-02T00:00:00Z");
  await importLegacyPurchases(admin, { commit: true });
  expect(await importLegacySubscriptions(admin, { commit: false })).toMatchObject({ subscriptions: "2", accounts: "1", customer_mappings: "1", committed: false, previouslyImported: false });
  expect((await admin.query("SELECT 1 FROM billing.subscriptions WHERE user_id=$1", [f.id])).rowCount).toBe(0);
  await expect(createBillingSummaryRepository(restricted).get({ version: 1, userId: f.id, licenseId: f.license })).rejects.toThrow();
  expect(await importLegacySubscriptions(admin, { commit: true })).toMatchObject({ committed: true, previouslyImported: false });
  expect(await importLegacySubscriptions(admin, { commit: true })).toMatchObject({ previouslyImported: true });
  expect((await admin.query("SELECT stripe_customer_id FROM billing.accounts WHERE user_id=$1", [f.id])).rows[0].stripe_customer_id).toBe(f.customer);
  expect((await admin.query("SELECT to_jsonb(s)=r.source_record same FROM stripe_subscriptions s JOIN billing.legacy_subscription_records r USING(stripe_subscription_id) WHERE s.user_id=$1", [f.id])).rows.every((row) => row.same)).toBe(true);
  expect((await admin.query("SELECT source_event_created_at::text AS epoch FROM billing.subscriptions WHERE stripe_subscription_id=$1", [active])).rows[0].epoch).toBe("1785542400");
  expect(await createBillingSummaryRepository(restricted).get({ version: 1, userId: f.id, licenseId: f.license })).toMatchObject({ subscription: { status: "active", customerPortalAvailable: true }, hasCompletedPurchase: false });
  expect((await admin.query("SELECT legacy_tier FROM licenses WHERE id=$1", [f.license])).rows[0].legacy_tier).toBe("max");
  expect((await admin.query("SELECT 1 FROM billing.entitlement_outbox WHERE user_id=$1", [f.id])).rowCount).toBe(0);
});
it("uses the latest purchase customer when there is no subscription and rejects history drift", async () => {
  const f = await fixture(), purchase = randomUUID();
  await admin.query(`INSERT INTO stripe_purchases(id,user_id,license_id,tier_purchased,stripe_checkout_session_id,stripe_customer_id,status)
    VALUES($1,$2,$3,'personal',$4,$5,'completed')`, [purchase, f.id, f.license, `cs_${purchase}`, f.customer]);
  await importLegacyPurchases(admin, { commit: true });
  await admin.query("UPDATE stripe_purchases SET amount=amount+1 WHERE id=$1", [purchase]);
  await expect(importLegacySubscriptions(admin, { commit: true })).rejects.toThrow("purchase history changed");
  await admin.query("UPDATE stripe_purchases SET amount=amount-1 WHERE id=$1", [purchase]);
  await importLegacySubscriptions(admin, { commit: true });
  expect((await admin.query("SELECT stripe_customer_id FROM billing.accounts WHERE user_id=$1", [f.id])).rows[0].stripe_customer_id).toBe(f.customer);
  expect(await createBillingSummaryRepository(restricted).get({ version: 1, userId: f.id, licenseId: f.license })).toMatchObject({ subscription: null, hasCompletedPurchase: true });
});
it("rejects conflicting customers or target data and never partially changes an import", async () => {
  const f = await fixture(), sub = await f.add(); await importLegacyPurchases(admin, { commit: true });
  await admin.query("INSERT INTO billing.accounts(user_id,license_id,stripe_customer_id) VALUES($1,$2,'cus_conflict')", [f.id, f.license]);
  await expect(importLegacySubscriptions(admin, { commit: true })).rejects.toThrow("account conflicts");
  expect((await admin.query("SELECT 1 FROM billing.legacy_subscription_records WHERE stripe_subscription_id=$1", [sub])).rowCount).toBe(0);
  await admin.query("DELETE FROM billing.accounts WHERE user_id=$1", [f.id]);
  const other = await fixture(); await other.add("active", f.customer);
  await expect(importLegacySubscriptions(admin, { commit: true })).rejects.toThrow("multiple accounts");
  expect((await admin.query("SELECT 1 FROM billing.cutover_checkpoints WHERE name='legacy_subscriptions_imported'")).rowCount).toBe(0);
});
it("detects post-import source changes and rejects overwriting changed native state", async () => {
  const f = await fixture(), sub = await f.add(); await importLegacyPurchases(admin, { commit: true }); await importLegacySubscriptions(admin, { commit: true });
  await admin.query("UPDATE stripe_subscriptions SET created_at=created_at-interval '1 day' WHERE stripe_subscription_id=$1", [sub]);
  await expect(importLegacySubscriptions(admin, { commit: false })).rejects.toThrow("changed after its snapshot");
  await admin.query("UPDATE stripe_subscriptions SET created_at=created_at+interval '1 day' WHERE stripe_subscription_id=$1", [sub]);
  await admin.query("UPDATE billing.subscriptions SET status='canceled' WHERE stripe_subscription_id=$1", [sub]);
  await expect(importLegacySubscriptions(admin, { commit: true })).rejects.toThrow("differs from the source");
  expect((await admin.query("SELECT status FROM billing.subscriptions WHERE stripe_subscription_id=$1", [sub])).rows[0].status).toBe("canceled");
  await admin.query("UPDATE billing.subscriptions SET status='active' WHERE stripe_subscription_id=$1", [sub]);
  await admin.query("DELETE FROM stripe_subscriptions WHERE stripe_subscription_id=$1", [sub]);
  await expect(importLegacySubscriptions(admin, { commit: false })).rejects.toThrow("verification failed or source changed");
});

it("rolls back account and subscription copies if durable snapshot persistence fails", async () => {
  const f = await fixture(), sub = await f.add(); await importLegacyPurchases(admin, { commit: true });
  await admin.query(`CREATE FUNCTION billing.test_reject_subscription_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic snapshot failure'; END $$;
    CREATE TRIGGER test_reject_subscription_snapshot BEFORE INSERT ON billing.legacy_subscription_records FOR EACH ROW EXECUTE FUNCTION billing.test_reject_subscription_snapshot()`);
  try { await expect(importLegacySubscriptions(admin, { commit: true })).rejects.toThrow("synthetic snapshot failure"); }
  finally { await admin.query("DROP TRIGGER test_reject_subscription_snapshot ON billing.legacy_subscription_records; DROP FUNCTION billing.test_reject_subscription_snapshot()"); }
  expect((await admin.query("SELECT 1 FROM billing.accounts WHERE user_id=$1", [f.id])).rowCount).toBe(0);
  expect((await admin.query("SELECT 1 FROM billing.subscriptions WHERE stripe_subscription_id=$1", [sub])).rowCount).toBe(0);
  expect((await admin.query("SELECT 1 FROM billing.cutover_checkpoints WHERE name='legacy_subscriptions_imported'")).rowCount).toBe(0);
  expect(await importLegacySubscriptions(admin, { commit: true })).toMatchObject({ committed: true });
});
