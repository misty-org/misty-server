import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import type Stripe from "stripe";
import { afterAll, beforeAll, expect, it, vi } from "vitest";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import type { CheckoutIntent } from "../../../../../packages/service-contracts/src/payments.js";
import { createPriceCatalog } from "../subscriptions/model.js";
import { createCheckoutRepository } from "./repository.js";
import { createCheckoutService } from "./service.js";
import { createCheckoutRecovery } from "./recovery.js";
import type { RecoverySession } from "./recovery.js";
import { createPortalService } from "./portal.js";

const admin = createTestDatabase();
const accounts: string[] = [];
let payments: Pool;
const catalog = createPriceCatalog([{ id: "price_pro_month", tier: "pro", interval: "month" }, { id: "price_max_month", tier: "max", interval: "month" }]);
const urls = { success: "https://misty.example/success", cancel: "https://misty.example/cancel", portalReturn: "https://misty.example/billing" };
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../migrations/", import.meta.url))), "billing");
  await admin.query(`DO $$ BEGIN
    IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_hono_payments_test') THEN
      CREATE ROLE misty_hono_payments_test NOLOGIN NOSUPERUSER NOBYPASSRLS;
    END IF;
  END $$;
  GRANT USAGE ON SCHEMA billing TO misty_hono_payments_test; GRANT SELECT ON billing.account_closures TO misty_hono_payments_test;
  GRANT SELECT,INSERT,UPDATE ON billing.accounts,billing.checkout_attempts,billing.subscriptions,
    billing.entitlement_versions,billing.entitlement_outbox,billing.legacy_checkout_recovery TO misty_hono_payments_test;`);
  await admin.query("GRANT SELECT ON billing.cutover_checkpoints,billing.legacy_purchases TO misty_hono_payments_test");
  await admin.query("INSERT INTO billing.cutover_checkpoints(name,evidence) VALUES('legacy_checkouts_imported','{}'),('legacy_purchases_imported','{}'),('legacy_subscriptions_imported','{}')");
  payments = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_hono_payments_test", max: 4 });
});
afterAll(async () => {
  if (payments) await payments.end();
  for (const table of ["entitlement_outbox", "entitlement_versions", "subscriptions", "checkout_attempts", "accounts"]) {
    await admin.query(`DELETE FROM billing.${table} WHERE user_id=ANY($1::text[])`, [accounts]);
  }
  await admin.query("DELETE FROM billing.cutover_checkpoints WHERE name IN ('legacy_checkouts_imported','legacy_purchases_imported','legacy_subscriptions_imported')");
  await admin.end();
});
function intent(): CheckoutIntent {
  const userId = `checkout_${randomUUID()}`;
  accounts.push(userId);
  return { version: 1, userId, licenseId: `license_${userId}`, email: "user@example.invalid", tier: "pro", interval: "month", trialEligible: true };
}
function fixture() {
  const sessions = new Map<string, { params: string; session: Pick<Stripe.Checkout.Session, "id" | "url" | "status" | "expires_at"> }>();
  let failAfterCreate = false;
  const gateway = {
    createSession: vi.fn(async (parameters: Stripe.Checkout.SessionCreateParams, key: string) => {
      let saved = sessions.get(key);
      if (!saved) {
        const id = `cs_${randomUUID()}`;
        saved = { params: JSON.stringify(parameters), session: { id, url: `https://checkout.stripe.com/c/pay/${id}`, status: "open", expires_at: parameters.expires_at! } };
        sessions.set(key, saved);
      }
      expect(JSON.stringify(parameters)).toBe(saved.params);
      if (failAfterCreate) { failAfterCreate = false; throw new Error("Response lost after Stripe created the session"); }
      return saved.session;
    }),
    retrieveSession: vi.fn(async (id: string) => {
      const saved = [...sessions.values()].find((value) => value.session.id === id);
      if (!saved) throw new Error("Missing fake Stripe session");
      return saved.session;
    }),
    retrieveSubscription: vi.fn(async (_id: string): Promise<unknown> => { throw new Error("Unexpected subscription lookup"); }),
    cancelSubscription: vi.fn(async (_id: string): Promise<unknown> => { throw new Error("Unexpected subscription cancellation"); }),
  };
  return { gateway, sessions, loseNextResponse: () => { failAfterCreate = true; },
    service: createCheckoutService({ repository: createCheckoutRepository(payments), catalog, urls, gateway }) };
}

it("creates a portal for the bound account's stored customer only", async () => {
  const input = intent();
  await createCheckoutRepository(payments).begin(input, catalog, urls);
  const customerId = `cus_${randomUUID()}`;
  await admin.query("UPDATE billing.accounts SET stripe_customer_id=$2 WHERE user_id=$1", [input.userId, customerId]);
  const createSession = vi.fn(async () => ({ url: "https://billing.stripe.com/test" }));
  const portal = createPortalService({ pool: payments, returnUrl: urls.portalReturn, createSession });
  await expect(portal.create(input.userId, "wrong-license", "request-id")).rejects.toThrow("portal_unavailable");
  expect(createSession).not.toHaveBeenCalled();
  expect(await portal.create(input.userId, input.licenseId, "request-id")).toBe("https://billing.stripe.com/test");
  expect(createSession).toHaveBeenCalledExactlyOnceWith(customerId, urls.portalReturn, "misty-portal-request-id");
});

it("makes concurrent checkout requests share a single durable attempt and Stripe idempotency key", async () => {
  const test = fixture();
  const input = intent();
  const results = await Promise.all(Array.from({ length: 8 }, () => test.service.create(input)));
  expect(new Set(results).size).toBe(1);
  expect(test.sessions.size).toBe(1);
  expect((await admin.query("SELECT count(*) FROM billing.checkout_attempts WHERE user_id=$1", [input.userId])).rows[0].count).toBe("1");
});

it("retries a lost Stripe response with the original parameters even when account input changes", async () => {
  const test = fixture();
  const input = intent();
  test.loseNextResponse();
  await expect(test.service.create(input)).rejects.toThrow("Response lost");
  const url = await test.service.create({ ...input, email: "changed@example.invalid", trialEligible: false });
  expect(url).toContain("https://checkout.stripe.com/");
  expect(test.sessions.size).toBe(1);
  expect(test.gateway.createSession).toHaveBeenCalledTimes(2);
  expect(test.gateway.createSession.mock.calls[1]?.[0].customer_email).toBe(input.email);
  expect(test.gateway.createSession.mock.calls[1]?.[0].subscription_data?.trial_period_days).toBe(14);
});

it("does not replace a completed checkout while its subscription webhook is missing", async () => {
  const test = fixture();
  const input = intent();
  await test.service.create(input);
  [...test.sessions.values()][0]!.session.status = "complete";
  await admin.query("UPDATE billing.checkout_attempts SET expires_at=now()-INTERVAL '1 second' WHERE user_id=$1", [input.userId]);
  await expect(test.service.create(input)).rejects.toThrow("subscription_exists");
  await expect(test.service.create(input)).rejects.toThrow("subscription_exists");
  expect(test.gateway.createSession).toHaveBeenCalledTimes(1);
  expect((await admin.query("SELECT status FROM billing.checkout_attempts WHERE user_id=$1", [input.userId])).rows[0].status).toBe("completed");
});

it("does not reuse an old ambiguous idempotency key or create a different plan concurrently", async () => {
  const test = fixture();
  const input = intent();
  test.loseNextResponse();
  await expect(test.service.create(input)).rejects.toThrow("Response lost");
  await expect(test.service.create({ ...input, tier: "max" })).rejects.toThrow("checkout_in_progress");
  await admin.query("UPDATE billing.checkout_attempts SET created_at=now()-INTERVAL '24 hours' WHERE user_id=$1", [input.userId]);
  await expect(test.service.create(input)).rejects.toThrow("checkout_recovery_required");
  expect(test.gateway.createSession).toHaveBeenCalledTimes(1);
});

it("reconciles a past-due subscription that recovered before replacement without canceling it", async () => {
  const test = fixture();
  const input = intent();
  await createCheckoutRepository(payments).begin(input, catalog, urls);
  const subscriptionId = `sub_${randomUUID()}`;
  await admin.query(`INSERT INTO billing.subscriptions(stripe_subscription_id,user_id,stripe_customer_id,stripe_price_id,
    tier,billing_interval,status,current_period_end) VALUES($1,$2,'cus_test','price_pro_month','pro','month','past_due',now()+INTERVAL '30 days')`, [subscriptionId, input.userId]);
  test.gateway.retrieveSubscription.mockResolvedValue({
    id: subscriptionId, customer: "cus_test", metadata: { user_id: input.userId, license_id: input.licenseId, kind: "subscription", tier: "pro", interval: "month" },
    status: "active", cancel_at_period_end: false,
    items: { data: [{ current_period_end: 1900000000, price: { id: "price_pro_month", recurring: { interval: "month" } } }] },
  });
  await expect(test.service.create(input)).rejects.toThrow("subscription_exists");
  expect(test.gateway.cancelSubscription).not.toHaveBeenCalled();
  expect(test.gateway.createSession).not.toHaveBeenCalled();
  expect((await admin.query("SELECT status FROM billing.subscriptions WHERE stripe_subscription_id=$1", [subscriptionId])).rows[0].status).toBe("active");
  expect((await admin.query("SELECT revision FROM billing.entitlement_versions WHERE user_id=$1", [input.userId])).rows[0].revision).toBe("1");
});

it("recovers an orphaned checkout across multiple listing pages without creating another session", async () => {
  const test = fixture();
  const input = intent();
  test.loseNextResponse();
  await expect(test.service.create(input)).rejects.toThrow("Response lost");
  await admin.query("UPDATE billing.checkout_attempts SET created_at=now()-INTERVAL '1 hour',expires_at=now()-INTERVAL '25 minutes' WHERE user_id=$1", [input.userId]);
  const saved = [...test.sessions.values()][0]!;
  const parameters = JSON.parse(saved.params) as Stripe.Checkout.SessionCreateParams;
  const session: RecoverySession = {
    ...saved.session, status: "expired", expires_at: Math.floor(Date.now() / 1000) - 1500,
    metadata: parameters.metadata as Record<string, string>, mode: "subscription", subscription: null, client_reference_id: input.userId,
  };
  const listSessions = vi.fn()
    .mockResolvedValueOnce({ data: [{ ...session, id: "cs_other_page", metadata: {} }], has_more: true })
    .mockResolvedValueOnce({ data: [session], has_more: false });
  const worker = createCheckoutRecovery({ pool: payments, catalog, listSessions, fetchSubscription: test.gateway.retrieveSubscription });
  expect(await worker.runOnce()).toBe(true);
  expect((await admin.query("SELECT recovery_cursor,status FROM billing.checkout_attempts WHERE user_id=$1", [input.userId])).rows[0])
    .toEqual({ recovery_cursor: "cs_other_page", status: "creating" });
  expect(await worker.runOnce()).toBe(true);
  expect(listSessions.mock.calls[1]?.[0].starting_after).toBe("cs_other_page");
  expect((await admin.query("SELECT status,stripe_checkout_session_id FROM billing.checkout_attempts WHERE user_id=$1", [input.userId])).rows[0])
    .toEqual({ status: "expired", stripe_checkout_session_id: session.id });
  expect(test.gateway.createSession).toHaveBeenCalledTimes(1);
});

it("keeps orphan recovery retryable through an outage and atomically restores a completed subscription", async () => {
  const test = fixture();
  const input = intent();
  test.loseNextResponse();
  await expect(test.service.create(input)).rejects.toThrow("Response lost");
  await admin.query("UPDATE billing.checkout_attempts SET created_at=now()-INTERVAL '1 hour',expires_at=now()-INTERVAL '25 minutes' WHERE user_id=$1", [input.userId]);
  const saved = [...test.sessions.values()][0]!;
  const parameters = JSON.parse(saved.params) as Stripe.Checkout.SessionCreateParams;
  const subscriptionId = `sub_${randomUUID()}`;
  const session: RecoverySession = {
    ...saved.session, status: "complete", expires_at: Math.floor(Date.now() / 1000) - 1500,
    metadata: parameters.metadata as Record<string, string>, mode: "subscription", subscription: subscriptionId, client_reference_id: input.userId,
  };
  const listSessions = vi.fn().mockRejectedValueOnce(new Error("Stripe outage")).mockResolvedValueOnce({ data: [session], has_more: false });
  test.gateway.retrieveSubscription.mockResolvedValue({
    id: subscriptionId, customer: `cus_${input.userId}`, metadata: parameters.metadata,
    status: "active", cancel_at_period_end: false,
    items: { data: [{ current_period_end: 1900000000, price: { id: "price_pro_month", recurring: { interval: "month" } } }] },
  });
  const worker = createCheckoutRecovery({ pool: payments, catalog, listSessions, fetchSubscription: test.gateway.retrieveSubscription });
  expect(await worker.runOnce()).toBe(true);
  expect(await worker.runOnce()).toBe(false);
  expect((await admin.query("SELECT status,recovery_failures FROM billing.checkout_attempts WHERE user_id=$1", [input.userId])).rows[0])
    .toEqual({ status: "creating", recovery_failures: 1 });
  await admin.query("UPDATE billing.checkout_attempts SET recovery_after=now()-INTERVAL '1 second' WHERE user_id=$1", [input.userId]);
  expect(await worker.runOnce()).toBe(true);
  expect((await admin.query("SELECT status FROM billing.checkout_attempts WHERE user_id=$1", [input.userId])).rows[0].status).toBe("completed");
  expect((await admin.query("SELECT status FROM billing.subscriptions WHERE user_id=$1", [input.userId])).rows[0].status).toBe("active");
  expect((await admin.query("SELECT revision FROM billing.entitlement_versions WHERE user_id=$1", [input.userId])).rows[0].revision).toBe("1");
  expect(test.gateway.createSession).toHaveBeenCalledTimes(1);
});
