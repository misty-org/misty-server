import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { generateKeyPair } from "jose";
import { pino } from "pino";
import { afterAll, afterEach, beforeAll, expect, it } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import type { PaymentEntitlementEvent, PurchaseReversalEvent, SubscriptionProjection } from "../../../../../packages/service-contracts/src/payments.js";
import { createEntitlementRepository } from "./repository.js";
import { createEntitlementEffects } from "./service.js";
import { createEntitlementExpiry } from "./expiry.js";
import { createApi } from "../../app.js";
import { signServiceAssertion } from "../../../../../packages/runtime/src/service-assertion.js";

const admin = createTestDatabase();
let application: Pool;
const users: string[] = [];
const start = new Date("2026-09-07T12:00:00Z");
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await admin.query(`DO $$ BEGIN
    IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_hono_app_test') THEN
      CREATE ROLE misty_hono_app_test NOLOGIN NOSUPERUSER NOBYPASSRLS;
    END IF;
  END $$;
  GRANT USAGE ON SCHEMA public TO misty_hono_app_test;
  GRANT SELECT,UPDATE ON users,licenses TO misty_hono_app_test;
  GRANT SELECT,INSERT,UPDATE ON payment_entitlement_inbox,payment_entitlement_projections,payment_purchase_reversals,
    hosted_ai_wallets,hosted_ai_reservations,hosted_ai_usage_ledger TO misty_hono_app_test;
  GRANT SELECT,UPDATE ON license_lifetime_grants TO misty_hono_app_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_app_test", max: 4 });
}, 60000);
afterEach(async () => {
  await admin.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
  await admin.query("DELETE FROM payment_entitlement_inbox WHERE user_id=ANY($1::text[])", [users]);
  users.length = 0;
});
afterAll(async () => { if (application) await application.end(); await admin.end(); });

async function fixture(legacyTier: string | null = null) {
  const userId = `effect_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
  const licenseId = `license_${userId}`;
  users.push(userId);
  await withTransaction(admin, async (tx) => {
    await tx.query("INSERT INTO users(id,email,password_hash,username,license_id) VALUES($1,$2,'test-only',$1,$3)", [userId, `${userId}@example.invalid`, licenseId]);
    await tx.query("INSERT INTO licenses(id,user_id,legacy_tier,tier) VALUES($1,$2,$3,'basic')", [licenseId, userId, legacyTier]);
  });
  let revision = 0;
  let now = start;
  const identity = () => ({ version: 1 as const, eventId: randomUUID(), userId, licenseId, revision: String(++revision), generatedAt: now.toISOString() });
  return { userId, licenseId, setTime: (time: Date) => { now = time; },
    repository: createEntitlementRepository({ pool: application, ...createEntitlementEffects({ now: () => now }) }),
    expiry: createEntitlementExpiry({ pool: application, now: () => now }),
    subscription: (overrides: Partial<SubscriptionProjection> = {}): PaymentEntitlementEvent => ({ ...identity(), subscription: {
      subscriptionId: `sub_${userId}`, tier: "pro", interval: "month", status: "active", currentPeriodEnd: "2026-10-07T12:00:00Z", cancelAtPeriodEnd: false, ...overrides,
    } }),
    reversal: (purchaseId: string): PurchaseReversalEvent => ({ ...identity(), kind: "purchase_reversal", purchaseId, reason: "refunded" }),
    license: async () => (await admin.query("SELECT tier,status,expires_at,trial_started_at,legacy_tier FROM licenses WHERE id=$1", [licenseId])).rows[0],
    wallet: async () => (await admin.query("SELECT weekly_allowance_microusd,weekly_remaining_microusd,reserved_microusd,reset_at FROM hosted_ai_wallets WHERE user_id=$1", [userId])).rows[0],
  };
}

it("applies trial state atomically, records trial eligibility once and preserves spent/reserved usage on activation", async () => {
  const account = await fixture();
  const trial = account.subscription({ status: "trialing" });
  expect(await account.repository.receive(trial)).toBe("applied");
  expect(await account.license()).toMatchObject({ tier: "pro", status: "trialing", trial_started_at: start, expires_at: new Date("2026-10-07T12:00:00Z") });
  await admin.query("UPDATE hosted_ai_wallets SET weekly_remaining_microusd=800000,reserved_microusd=50000 WHERE user_id=$1", [account.userId]);
  account.setTime(new Date(start.getTime() + 3600_000));
  await account.repository.receive(account.subscription({ tier: "max" }));
  expect(await account.wallet()).toMatchObject({ weekly_allowance_microusd: "1800000", weekly_remaining_microusd: "1700000", reserved_microusd: "50000" });
  expect(await account.license()).toMatchObject({ tier: "max", status: "active", trial_started_at: start, expires_at: null });
  expect(await account.repository.receive(trial)).toBe("duplicate");
  expect((await admin.query("SELECT count(*) FROM hosted_ai_usage_ledger WHERE user_id=$1", [account.userId])).rows[0].count).toBe("2");
});

it("retains consumed usage on downgrade and makes one UTC weekly grant while keeping live reservations", async () => {
  const account = await fixture();
  await account.repository.receive(account.subscription());
  await admin.query("UPDATE hosted_ai_wallets SET weekly_remaining_microusd=800000,reserved_microusd=60000 WHERE user_id=$1", [account.userId]);
  await admin.query(`INSERT INTO hosted_ai_reservations(id,user_id,idempotency_key,meter,reserved_microusd,created_at)
    VALUES($1,$2,$1,'assistant_ai',20000,$3)`, [randomUUID(), account.userId, new Date(start.getTime() - 16 * 60_000)]);
  await account.repository.receive(account.subscription({ status: "canceled" }));
  expect(await account.wallet()).toMatchObject({ weekly_allowance_microusd: "150000", weekly_remaining_microusd: "50000", reserved_microusd: "40000" });
  account.setTime(new Date("2026-09-14T00:00:00Z"));
  const renewal = account.subscription({ status: "canceled" });
  await account.repository.receive(renewal);
  await account.repository.receive(renewal);
  expect(await account.wallet()).toMatchObject({ weekly_remaining_microusd: "150000", reserved_microusd: "40000", reset_at: new Date("2026-09-21T00:00:00Z") });
  expect((await admin.query("SELECT count(*) FROM hosted_ai_usage_ledger WHERE user_id=$1 AND source='weekly_grant'", [account.userId])).rows[0].count).toBe("2");
});

it("expires access once at the fail-safe and restores it when a newer canonical billing period arrives", async () => {
  const account = await fixture("max");
  await account.repository.receive(account.subscription({ currentPeriodEnd: start.toISOString() }));
  account.setTime(new Date(start.getTime() + 72 * 3600_000 - 1));
  expect(await account.expiry.runOnce()).toBe(false);
  account.setTime(new Date(start.getTime() + 72 * 3600_000));
  expect((await Promise.all([account.expiry.runOnce(), account.expiry.runOnce()])).filter(Boolean)).toHaveLength(1);
  expect(await account.license()).toMatchObject({ tier: "max", status: "active" });
  expect((await admin.query("SELECT payload->'subscription'->>'status' status,access_expires_at FROM payment_entitlement_projections WHERE user_id=$1", [account.userId])).rows[0])
    .toEqual({ status: "active", access_expires_at: null });
  await account.repository.receive(account.subscription());
  expect(await account.license()).toMatchObject({ tier: "pro", status: "active" });
});

it("expires a trial at its own deadline without clearing trial history", async () => {
  const account = await fixture();
  const end = new Date(start.getTime() + 3600_000);
  await account.repository.receive(account.subscription({ status: "trialing", currentPeriodEnd: end.toISOString() }));
  account.setTime(end);
  expect(await account.expiry.runOnce()).toBe(true);
  expect(await account.license()).toMatchObject({ tier: "basic", status: "active", trial_started_at: start, expires_at: null });
});

it("revokes only the attributed purchase while preserving manual and active subscription access", async () => {
  const account = await fixture("max");
  const purchaseId = randomUUID();
  await admin.query(`INSERT INTO license_lifetime_grants(id,user_id,license_id,source,source_id,tier) VALUES
    ($1,$2,$3,'purchase',$4,'max'),($5::uuid,$2,$3,'manual',$5::text,'pro')`, [randomUUID(), account.userId, account.licenseId, purchaseId, randomUUID()]);
  const reversal = account.reversal(purchaseId);
  await account.repository.receive(account.subscription({ tier: "max" }));
  await account.repository.receive(reversal);
  expect(await account.license()).toMatchObject({ tier: "max", legacy_tier: "pro" });
  await account.repository.receive(account.subscription({ status: "canceled" }));
  expect(await account.license()).toMatchObject({ tier: "pro", legacy_tier: "pro" });
  expect(await account.repository.receive(reversal)).toBe("duplicate");
});

it("keeps an unattributed purchase pending and rolls back its inbox until the grant is verified", async () => {
  const account = await fixture("max");
  const purchaseId = randomUUID();
  const event = account.reversal(purchaseId);
  await expect(account.repository.receive(event)).rejects.toThrow("attribution requires verification");
  expect((await admin.query("SELECT 1 FROM payment_entitlement_inbox WHERE event_id=$1", [event.eventId])).rowCount).toBe(0);
  expect((await admin.query("SELECT 1 FROM payment_purchase_reversals WHERE purchase_id=$1", [purchaseId])).rowCount).toBe(0);
  expect(await account.license()).toMatchObject({ legacy_tier: "max" });
  await admin.query("INSERT INTO license_lifetime_grants(id,user_id,license_id,source,source_id,tier) VALUES($1,$2,$3,'purchase',$4,'max')", [randomUUID(), account.userId, account.licenseId, purchaseId]);
  expect(await account.repository.receive(event)).toBe("applied");
  expect(await account.license()).toMatchObject({ tier: "basic", legacy_tier: null });
});

it("composes the private HTTP receiver with native effects and refuses app credentials", async () => {
  const account = await fixture();
  const keys = await generateKeyPair("EdDSA", { crv: "Ed25519" });
  const app = createApi({ logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => false,
    migrationComplete: false, entitlements: { publicKeys: new Map([["payments-test", keys.publicKey]]), repository: account.repository },
  });
  const path = "/internal/payments/entitlements";
  const event = account.subscription();
  const body = JSON.stringify(event);
  const token = await signServiceAssertion({ privateKey: keys.privateKey, keyId: "payments-test", issuer: "misty-payments",
    audience: "misty-api", scope: "entitlements:write", subject: account.userId, request: { method: "POST", path, body: Buffer.from(body) },
  });
  const send = (authorization: string) => app.request(path, { method: "POST", body, headers: { Authorization: `Bearer ${authorization}`, "Content-Type": "application/json" } });
  expect((await send("downloaded-app-session")).status).toBe(401);
  expect(await account.license()).toMatchObject({ tier: "basic" });
  expect((await send(token)).status).toBe(200);
  expect(await account.license()).toMatchObject({ tier: "pro", status: "active" });
  expect(await (await send(token)).json()).toEqual({ eventId: event.eventId, result: "duplicate" });
});
it("acknowledges ordered payment snapshots for inactive accounts without renewing access or allowance", async () => {
  for (const state of ["pending_deletion", "deleted"] as const) {
    const account = await fixture(); await account.repository.receive(account.subscription());
    const license = await account.license(), wallet = await account.wallet();
    await admin.query("UPDATE users SET lifecycle_state=$2 WHERE id=$1", [account.userId, state]);
    const late = account.subscription({ tier: "max", status: "trialing" });
    expect(await account.repository.receive(late)).toBe("superseded"); expect(await account.repository.receive(late)).toBe("duplicate");
    const closed = { ...account.subscription(), subscription: null }; expect(await account.repository.receive(closed)).toBe("superseded");
    expect(await account.license()).toEqual(license); expect(await account.wallet()).toEqual(wallet);
    expect((await admin.query("SELECT revision,access_expires_at FROM payment_entitlement_projections WHERE user_id=$1", [account.userId])).rows[0]).toEqual({ revision: closed.revision, access_expires_at: null });
    await expect(account.repository.receive({ ...late, licenseId: "wrong" })).rejects.toThrow("Account is unavailable");
    await expect(account.repository.receive({ ...late, subscription: null })).rejects.toThrow("conflicts");
  }
});
it("retains attributed financial reversals after deletion without refreshing the wallet", async () => {
  const account = await fixture("max"), purchaseId = randomUUID();
  await admin.query("INSERT INTO license_lifetime_grants(id,user_id,license_id,source,source_id,tier) VALUES($1,$2,$3,'purchase',$4,'max')", [randomUUID(), account.userId, account.licenseId, purchaseId]);
  await account.repository.receive(account.subscription()); const wallet = await account.wallet();
  await admin.query("UPDATE users SET lifecycle_state='deleted' WHERE id=$1", [account.userId]);
  const event = account.reversal(purchaseId); expect(await account.repository.receive(event)).toBe("applied"); expect(await account.repository.receive(event)).toBe("duplicate");
  expect((await admin.query("SELECT revoked_at,reversal_event_id FROM license_lifetime_grants WHERE source_id=$1", [purchaseId])).rows[0]).toMatchObject({ revoked_at: start, reversal_event_id: event.eventId });
  expect(await account.license()).toMatchObject({ legacy_tier: null }); expect(await account.wallet()).toEqual(wallet);
});
