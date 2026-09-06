import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { generateKeyPair } from "jose";
import { Hono } from "hono";
import { Pool } from "pg";
import { afterAll, beforeAll, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { createEntitlementRepository } from "../../../../api/src/modules/entitlements/repository.js";
import { createEntitlementRoutes } from "../../../../api/src/modules/entitlements/routes.js";
import { createEntitlementOutbox, enqueueEntitlement, enqueuePurchaseReversal } from "./repository.js";
import { createEntitlementDispatcher } from "./dispatcher.js";

const admin = createTestDatabase();
const userId = `billing_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
const licenseId = `license_${userId}`;
let payments: Pool;
let application: Pool;

beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../migrations/", import.meta.url))), "billing");
  await admin.query(`DO $$ BEGIN
    IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_hono_payments_test') THEN
      CREATE ROLE misty_hono_payments_test NOLOGIN NOSUPERUSER NOBYPASSRLS;
    END IF;
    IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_hono_app_test') THEN
      CREATE ROLE misty_hono_app_test NOLOGIN NOSUPERUSER NOBYPASSRLS;
    END IF;
  END $$;
  GRANT USAGE ON SCHEMA billing TO misty_hono_payments_test; GRANT SELECT ON billing.account_closures TO misty_hono_payments_test;
  GRANT SELECT,INSERT,UPDATE ON billing.accounts,billing.entitlement_versions,billing.entitlement_outbox TO misty_hono_payments_test;
  GRANT USAGE ON SCHEMA public TO misty_hono_app_test;
  GRANT SELECT,UPDATE ON public.users,public.licenses TO misty_hono_app_test;
  GRANT SELECT,INSERT,UPDATE ON public.payment_entitlement_inbox,public.payment_entitlement_projections,public.payment_purchase_reversals TO misty_hono_app_test;`);
  await withTransaction(admin, async (tx) => {
    await tx.query("INSERT INTO users(id,email,password_hash,username,license_id) VALUES($1,$2,'test-only',$1,$3)", [userId, `${userId}@example.invalid`, licenseId]);
    await tx.query("INSERT INTO licenses(id,user_id) VALUES($1,$2)", [licenseId, userId]);
    await tx.query("INSERT INTO billing.accounts(user_id,license_id) VALUES($1,$2)", [userId, licenseId]);
  });
  payments = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_payments_test", max: 2 });
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_app_test", max: 2 });
}, 60000);
afterAll(async () => {
  if (payments) await payments.end();
  if (application) await application.end();
  await admin.query("DELETE FROM users WHERE id=$1", [userId]);
  await admin.query("DELETE FROM payment_entitlement_inbox WHERE user_id=$1", [userId]);
  await admin.query("DELETE FROM billing.entitlement_outbox WHERE user_id=$1", [userId]);
  await admin.query("DELETE FROM billing.entitlement_versions WHERE user_id=$1", [userId]);
  await admin.query("DELETE FROM billing.accounts WHERE user_id=$1", [userId]);
  await admin.end();
});

it("delivers signed events, recovers a lost acknowledgement, and rejects older entitlement revisions", async () => {
  const keys = await generateKeyPair("EdDSA", { crv: "Ed25519" });
  let effects = 0;
  const repository = createEntitlementRepository({
    pool: application,
    applyPurchaseReversal: async () => { throw new Error("Unexpected reversal"); },
    applyEntitlements: async (tx, event) => {
      // A test effect verifies the inbox and its consumer commit as one transaction.
      await tx.query("UPDATE licenses SET tier=$2 WHERE id=$1", [event.licenseId, event.subscription?.tier ?? "basic"]);
      effects++;
    },
  });
  const api = new Hono().route("/internal/payments/entitlements", createEntitlementRoutes({
    publicKeys: new Map([["payments-key-1", keys.publicKey]]), repository,
  }));
  const outbox = createEntitlementOutbox(payments);
  const first = await withTransaction(payments, (tx) => enqueueEntitlement(tx, {
    userId, licenseId, subscription: { subscriptionId: "sub_test", tier: "pro", interval: "month", status: "active",
      currentPeriodEnd: new Date(Date.now() + 86400000).toISOString(), cancelAtPeriodEnd: false },
  }));
  let loseResponse = true;
  const dispatch = createEntitlementDispatcher({
    endpoint: "https://api.example.invalid/internal/payments/entitlements", privateKey: keys.privateKey, keyId: "payments-key-1", outbox,
    fetch: async (input, init) => {
      const response = await api.request(new Request(String(input), init));
      if (loseResponse) { loseResponse = false; throw new Error("Connection closed after API commit"); }
      return response;
    },
  });
  expect(await dispatch.runOnce()).toBe(true);
  expect(effects).toBe(1);
  await admin.query("UPDATE billing.entitlement_outbox SET available_at=now()-INTERVAL '1 second' WHERE event_id=$1", [first.eventId]);
  expect(await dispatch.runOnce()).toBe(true);
  expect(effects).toBe(1);
  expect((await admin.query("SELECT state FROM billing.entitlement_outbox WHERE event_id=$1", [first.eventId])).rows[0].state).toBe("delivered");

  const older = await withTransaction(payments, (tx) => enqueueEntitlement(tx, { userId, licenseId, subscription: first.subscription }));
  const newest = await withTransaction(payments, (tx) => enqueueEntitlement(tx, { userId, licenseId, subscription: null }));
  expect(await repository.receive(newest)).toBe("applied");
  expect(await repository.receive(older)).toBe("superseded");
  expect(effects).toBe(2);
  expect((await admin.query("SELECT tier FROM licenses WHERE id=$1", [licenseId])).rows[0].tier).toBe("basic");
  await expect(repository.receive({ ...newest, eventId: randomUUID() })).rejects.toThrow("conflicts");
  await expect(repository.receive({ ...newest, subscription: first.subscription })).rejects.toThrow("conflicts");
  await expect(repository.receive({ ...newest, eventId: randomUUID(), revision: "4", licenseId: "another-license" })).rejects.toThrow("unavailable");
});

it("rolls back the inbox and projection when entitlement application fails", async () => {
  const event = await withTransaction(payments, (tx) => enqueueEntitlement(tx, { userId, licenseId, subscription: null }));
  const repository = createEntitlementRepository({ pool: application, applyEntitlements: async () => { throw new Error("test effect failed"); }, applyPurchaseReversal: async () => { throw new Error("Unexpected reversal"); } });
  await expect(repository.receive(event)).rejects.toThrow("test effect failed");
  expect((await admin.query("SELECT 1 FROM payment_entitlement_inbox WHERE event_id=$1", [event.eventId])).rowCount).toBe(0);
  expect((await admin.query("SELECT revision FROM payment_entitlement_projections WHERE user_id=$1", [userId])).rows[0].revision).not.toBe(event.revision);
});

it("applies an older purchase reversal after a newer subscription and deduplicates by purchase", async () => {
  const purchaseId = randomUUID();
  const reversal = await withTransaction(payments, (tx) => enqueuePurchaseReversal(tx, { userId, licenseId, purchaseId, reason: "refunded" }));
  const newer = await withTransaction(payments, (tx) => enqueueEntitlement(tx, { userId, licenseId, subscription: null }));
  let reversals = 0;
  const repository = createEntitlementRepository({ pool: application, applyEntitlements: async () => {},
    applyPurchaseReversal: async () => { reversals++; },
  });
  expect(await repository.receive(newer)).toBe("applied");
  expect(await repository.receive(reversal)).toBe("applied");
  expect(await repository.receive(reversal)).toBe("duplicate");
  const dispute = await withTransaction(payments, (tx) => enqueuePurchaseReversal(tx, { userId, licenseId, purchaseId, reason: "disputed" }));
  expect(await repository.receive(dispute)).toBe("duplicate");
  expect(reversals).toBe(1);
  expect((await admin.query("SELECT revision FROM payment_entitlement_projections WHERE user_id=$1", [userId])).rows[0].revision).toBe(newer.revision);
});

it("retries a purchase reversal when its transactional consumer fails", async () => {
  const event = await withTransaction(payments, (tx) => enqueuePurchaseReversal(tx, { userId, licenseId, purchaseId: randomUUID(), reason: "disputed" }));
  let fail = true;
  const repository = createEntitlementRepository({ pool: application, applyEntitlements: async () => {}, applyPurchaseReversal: async () => {
    if (fail) throw new Error("Reversal consumer unavailable");
  } });
  await expect(repository.receive(event)).rejects.toThrow("Reversal consumer unavailable");
  expect((await admin.query("SELECT 1 FROM payment_entitlement_inbox WHERE event_id=$1", [event.eventId])).rowCount).toBe(0);
  expect((await admin.query("SELECT 1 FROM payment_purchase_reversals WHERE purchase_id=$1", [event.purchaseId])).rowCount).toBe(0);
  fail = false;
  expect(await repository.receive(event)).toBe("applied");
});
