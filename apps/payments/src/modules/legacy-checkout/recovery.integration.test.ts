import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Pool } from "pg";
import { afterAll, afterEach, beforeAll, expect, it, vi } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { assertRuntimeDatabaseRole } from "../../../../../packages/database/src/roles.js";
import { importLegacyPurchases } from "../legacy-purchases/import.js";
import { importLegacySubscriptions } from "../subscriptions/import.js";
import { createCheckoutRepository } from "../checkout/repository.js";
import { createCheckoutService } from "../checkout/service.js";
import { createPriceCatalog } from "../subscriptions/model.js";
import { importLegacyCheckouts } from "./import.js";
import { legacyCheckoutRecoveryReport, retryLegacyCheckoutReview } from "./operations.js";
import { createLegacyCheckoutRecovery } from "./recovery.js";

const admin = createTestDatabase(), users: string[] = [], attempts: string[] = [];
let payments: Pool;
const catalog = createPriceCatalog([{ id: "price_legacy", tier: "pro", interval: "month" }]);
const urls = { success: "https://misty.example/success", cancel: "https://misty.example/cancel", portalReturn: "https://misty.example/billing" };
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../migrations/", import.meta.url))), "billing");
  await admin.query(`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_legacy_checkout_test') THEN
    CREATE ROLE misty_legacy_checkout_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF; END $$;
    GRANT USAGE ON SCHEMA billing TO misty_legacy_checkout_test; GRANT SELECT ON billing.account_closures TO misty_legacy_checkout_test;
    GRANT SELECT ON billing.cutover_checkpoints,billing.legacy_checkout_records,billing.legacy_purchases TO misty_legacy_checkout_test;
    GRANT SELECT,INSERT,UPDATE ON billing.accounts,billing.checkout_attempts,billing.subscriptions,billing.entitlement_versions,billing.entitlement_outbox,billing.legacy_checkout_recovery TO misty_legacy_checkout_test;`);
  payments = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_legacy_checkout_test", max: 4 });
}, 60000);
afterEach(async () => {
  for (const table of ["entitlement_outbox", "entitlement_versions", "subscriptions", "checkout_attempts", "legacy_checkout_recovery", "accounts"]) {
    await admin.query(`DELETE FROM billing.${table} WHERE user_id=ANY($1::text[])`, [users]);
  }
  await admin.query("DELETE FROM billing.legacy_checkout_records WHERE id=ANY($1::text[])", [attempts]);
  await admin.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]);
  await admin.query("DELETE FROM billing.cutover_checkpoints WHERE name IN ('legacy_purchases_imported','legacy_subscriptions_imported','legacy_checkouts_imported')");
  users.length = 0; attempts.length = 0;
});
afterAll(async () => { if (payments) await payments.end(); await admin.end(); });
async function fixture(options: { status?: string; known?: boolean; future?: boolean } = {}) {
  const userId = `old_checkout_${randomUUID().replaceAll("-", "").slice(0, 12)}`, licenseId = `license_${userId}`;
  const attemptId = `go-attempt-${randomUUID()}`, sessionId = `cs_${randomUUID()}`, subscriptionId = `sub_${randomUUID()}`;
  const created = new Date(Date.now() - (options.future ? 60 : 3600) * 1000); created.setMilliseconds(0);
  const end = new Date(created.getTime() + 35 * 60000);
  users.push(userId); attempts.push(attemptId);
  await withTransaction(admin, async (tx) => {
    await tx.query("INSERT INTO users(id,email,password_hash,username,license_id) VALUES($1,$2,'test-only',$1,$3)", [userId, `${userId}@example.invalid`, licenseId]);
    await tx.query("INSERT INTO licenses(id,user_id) VALUES($1,$2)", [licenseId, userId]);
    await tx.query(`INSERT INTO stripe_subscription_checkout_attempts(id,user_id,license_id,tier,billing_interval,status,stripe_checkout_session_id,checkout_url,created_at,expires_at)
      VALUES($1,$2,$3,'pro','month',$4,$5,'https://unverified.example/old-checkout',$6,$7)`,
      [attemptId, userId, licenseId, options.status ?? "creating", options.known ? sessionId : null, created, end]);
  }, { mode: "service" });
  const metadata = { user_id: userId, license_id: licenseId, tier: "pro", interval: "month", kind: "subscription" };
  const session = { id: sessionId, status: options.future ? "open" : "expired", mode: "subscription", metadata, client_reference_id: userId,
    created: created.getTime() / 1000 + 1, expires_at: end.getTime() / 1000, subscription: null as string | null, customer: `cus_${userId}`, url: "https://checkout.stripe.com/c/pay/verified" };
  const canonical = { id: subscriptionId, customer: `cus_${userId}`, metadata, status: "active", cancel_at_period_end: false,
    items: { data: [{ current_period_end: Math.floor(Date.now() / 1000) + 30 * 86400, price: { id: "price_legacy", recurring: { interval: "month" } } }] } };
  const intent = { version: 1 as const, userId, licenseId, email: `${userId}@example.invalid`, tier: "pro" as const, interval: "month" as const, trialEligible: false };
  const listSessions = vi.fn(async (_query: unknown) => ({ data: [session], has_more: false }));
  const retrieveSession = vi.fn(async (_id: string) => structuredClone(session));
  const fetchSubscription = vi.fn(async (_id: string) => structuredClone(canonical));
  const createSession = vi.fn(async () => { throw new Error("Unexpected checkout creation"); });
  const cancelSubscription = vi.fn(async () => { throw new Error("Unexpected subscription cancellation"); });
  const worker = () => createLegacyCheckoutRecovery({ pool: payments, catalog, listSessions, retrieveSession, fetchSubscription });
  return { userId, licenseId, attemptId, sessionId, subscriptionId, intent, session, canonical, listSessions, retrieveSession, fetchSubscription, createSession, cancelSubscription, worker,
    service: createCheckoutService({ repository: createCheckoutRepository(payments), catalog, urls,
      gateway: { createSession, retrieveSession: async () => { throw new Error("Unexpected native session lookup"); }, retrieveSubscription: fetchSubscription, cancelSubscription } }),
    state: async () => (await admin.query("SELECT * FROM billing.legacy_checkout_recovery WHERE id=$1", [attemptId])).rows[0],
    due: async () => admin.query("UPDATE billing.legacy_checkout_recovery SET recovery_after=now() WHERE id=$1", [attemptId]),
  };
}
async function imports() { await importLegacyPurchases(admin, { commit: true }); await importLegacySubscriptions(admin, { commit: true }); await importLegacyCheckouts(admin, { commit: true }); }

it("requires verified history, preserves non-UUID Go IDs and exact records, and never exposes an unverified URL", async () => {
  const f = await fixture({ known: true, status: "open", future: true });
  await expect(f.service.create(f.intent)).rejects.toThrow("checkout_recovery_required");
  expect(await f.worker().runOnce()).toBe(false);
  await expect(importLegacyCheckouts(admin, { commit: false })).rejects.toThrow("Verify the legacy subscription import first");
  await importLegacyPurchases(admin, { commit: true }); await importLegacySubscriptions(admin, { commit: true });
  expect(await importLegacyCheckouts(admin, { commit: false })).toMatchObject({ attempts: "1", accounts: "1", committed: false });
  expect(await f.state()).toBeUndefined();
  await importLegacyCheckouts(admin, { commit: true });
  expect(await importLegacyCheckouts(admin, { commit: true })).toMatchObject({ previouslyImported: true });
  expect((await admin.query("SELECT to_jsonb(c)=r.source_record same FROM stripe_subscription_checkout_attempts c JOIN billing.legacy_checkout_records r USING(id) WHERE c.id=$1", [f.attemptId])).rows[0].same).toBe(true);
  expect(await f.state()).toMatchObject({ state: "pending", checkout_url: "", stripe_checkout_session_id: null });
  await expect(f.service.create(f.intent)).rejects.toThrow("checkout_recovery_required");
  expect(f.createSession).not.toHaveBeenCalled();
  await assertRuntimeDatabaseRole(payments, "payments");
  await expect(importLegacyCheckouts(payments, { commit: true })).rejects.toThrow("permission denied");
  await expect(payments.query("SELECT * FROM users")).rejects.toThrow("permission denied");
  await expect(payments.query("UPDATE billing.legacy_checkout_records SET source_record='{}'")).rejects.toThrow("permission denied");
  await admin.query("GRANT UPDATE ON billing.legacy_checkout_records TO misty_legacy_checkout_test");
  try { await expect(assertRuntimeDatabaseRole(payments, "payments")).rejects.toThrow("service isolation"); }
  finally { await admin.query("REVOKE UPDATE ON billing.legacy_checkout_records FROM misty_legacy_checkout_test"); }
});

it("rolls back copies and the checkpoint on late failure, rejects source drift, and preserves recovery progress on a repeat import", async () => {
  const f = await fixture();
  await importLegacyPurchases(admin, { commit: true }); await importLegacySubscriptions(admin, { commit: true });
  await admin.query(`CREATE FUNCTION billing.test_reject_legacy_checkout() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic legacy checkout failure'; END $$;
    CREATE TRIGGER test_reject_legacy_checkout BEFORE INSERT ON billing.legacy_checkout_recovery FOR EACH ROW EXECUTE FUNCTION billing.test_reject_legacy_checkout()`);
  try { await expect(importLegacyCheckouts(admin, { commit: true })).rejects.toThrow("synthetic legacy checkout failure"); }
  finally { await admin.query("DROP TRIGGER test_reject_legacy_checkout ON billing.legacy_checkout_recovery; DROP FUNCTION billing.test_reject_legacy_checkout()"); }
  expect((await admin.query("SELECT 1 FROM billing.accounts WHERE user_id=$1", [f.userId])).rowCount).toBe(0);
  expect((await admin.query("SELECT 1 FROM billing.legacy_checkout_records WHERE id=$1", [f.attemptId])).rowCount).toBe(0);
  expect((await admin.query("SELECT 1 FROM billing.cutover_checkpoints WHERE name='legacy_checkouts_imported'")).rowCount).toBe(0);
  await importLegacyCheckouts(admin, { commit: true }); await f.worker().runOnce();
  expect(await f.state()).toMatchObject({ state: "verified", status: "expired" });
  await importLegacyCheckouts(admin, { commit: true });
  expect(await f.state()).toMatchObject({ state: "verified", status: "expired" });
  await admin.query("UPDATE stripe_subscription_checkout_attempts SET checkout_url='https://changed.example' WHERE id=$1", [f.attemptId]);
  await expect(importLegacyCheckouts(admin, { commit: false })).rejects.toThrow("original snapshot");
});

it("returns only a canonically verified old open session and resolves completion without any checkout creation or cancellation", async () => {
  const f = await fixture({ status: "open", known: true, future: true }); await imports();
  expect(await f.worker().runOnce()).toBe(true);
  expect(await f.service.create(f.intent)).toBe(f.session.url);
  await expect(f.service.create({ ...f.intent, tier: "max" })).rejects.toThrow("checkout_in_progress");
  expect(f.listSessions).not.toHaveBeenCalled(); expect(f.createSession).not.toHaveBeenCalled(); expect(f.cancelSubscription).not.toHaveBeenCalled();
  f.session.status = "complete"; f.session.subscription = f.subscriptionId; await f.due();
  expect(await f.worker().runOnce()).toBe(true);
  expect(await f.state()).toMatchObject({ state: "verified", status: "completed" });
  expect((await admin.query("SELECT status FROM billing.subscriptions WHERE user_id=$1", [f.userId])).rows[0].status).toBe("active");
  expect((await admin.query("SELECT count(*)::text count FROM billing.entitlement_outbox WHERE user_id=$1", [f.userId])).rows[0].count).toBe("1");
  await expect(f.service.create(f.intent)).rejects.toThrow("subscription_exists");
});

it("retains a candidate across worker restarts, exhausts all pages, then retrieves fresh canonical completion", async () => {
  const f = await fixture(); await imports();
  f.listSessions.mockResolvedValueOnce({ data: [f.session], has_more: true })
    .mockResolvedValueOnce({ data: [{ ...f.session, id: "cs_unrelated", client_reference_id: "other", metadata: { ...f.session.metadata, user_id: "other" } }], has_more: false });
  expect(await f.worker().runOnce()).toBe(true);
  expect(await f.state()).toMatchObject({ state: "pending", recovery_cursor: f.sessionId, candidate_session_id: f.sessionId });
  expect(f.retrieveSession).not.toHaveBeenCalled();
  await expect(f.service.create(f.intent)).rejects.toThrow("checkout_recovery_required");
  f.session.status = "complete"; f.session.subscription = f.subscriptionId;
  expect(await f.worker().runOnce()).toBe(true);
  expect(f.listSessions.mock.calls[1]?.[0]).toMatchObject({ starting_after: f.sessionId, limit: 100 });
  expect(await f.state()).toMatchObject({ state: "verified", status: "completed", recovery_cursor: null });
  expect(f.retrieveSession).toHaveBeenCalledExactlyOnceWith(f.sessionId);
  expect(f.createSession).not.toHaveBeenCalled();
});

it("holds ambiguous matches found on different pages for review instead of selecting the first", async () => {
  const f = await fixture(); await imports();
  f.listSessions.mockResolvedValueOnce({ data: [f.session], has_more: true }).mockResolvedValueOnce({ data: [{ ...f.session, id: "cs_second_match" }], has_more: false });
  await f.worker().runOnce(); await f.worker().runOnce();
  expect(await f.state()).toMatchObject({ state: "review", last_recovery_error: "legacy_checkout_ambiguous" });
  expect(await f.worker().runOnce()).toBe(false);
  expect(f.retrieveSession).not.toHaveBeenCalled();
  await expect(f.service.create(f.intent)).rejects.toThrow("checkout_recovery_required");
  expect((await admin.query("SELECT 1 FROM billing.entitlement_outbox WHERE user_id=$1", [f.userId])).rowCount).toBe(0);
});

it("backs off provider failure and atomically rolls back a failed completed-subscription recovery before retry", async () => {
  const f = await fixture({ known: true }); await imports();
  f.session.status = "complete"; f.session.subscription = f.subscriptionId;
  f.fetchSubscription.mockRejectedValueOnce(new Error("Stripe unavailable"));
  expect(await f.worker().runOnce()).toBe(true);
  expect(await f.worker().runOnce()).toBe(false);
  expect(await f.state()).toMatchObject({ state: "pending", recovery_failures: 1, last_recovery_error: "legacy_checkout_unavailable" });
  expect((await admin.query("SELECT 1 FROM billing.subscriptions WHERE user_id=$1", [f.userId])).rowCount).toBe(0);
  expect((await admin.query("SELECT 1 FROM billing.entitlement_outbox WHERE user_id=$1", [f.userId])).rowCount).toBe(0);
  await admin.query(`CREATE FUNCTION billing.test_reject_recovered_checkout() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic recovery persistence failure'; END $$;
    CREATE TRIGGER test_reject_recovered_checkout BEFORE UPDATE ON billing.legacy_checkout_recovery FOR EACH ROW WHEN (NEW.state='verified') EXECUTE FUNCTION billing.test_reject_recovered_checkout()`);
  try { await f.due(); await f.worker().runOnce(); }
  finally { await admin.query("DROP TRIGGER test_reject_recovered_checkout ON billing.legacy_checkout_recovery; DROP FUNCTION billing.test_reject_recovered_checkout()"); }
  expect(await f.state()).toMatchObject({ state: "pending", recovery_failures: 2 });
  expect((await admin.query("SELECT 1 FROM billing.subscriptions WHERE user_id=$1", [f.userId])).rowCount).toBe(0);
  expect((await admin.query("SELECT 1 FROM billing.entitlement_outbox WHERE user_id=$1", [f.userId])).rowCount).toBe(0);
  await f.due(); await f.worker().runOnce();
  expect(await f.state()).toMatchObject({ state: "verified", status: "completed", recovery_failures: 0 });
});

it("only releases an absent creation after the full expired window and flags a missing completed session", async () => {
  const f = await fixture(); await imports();
  f.listSessions.mockResolvedValueOnce({ data: [{ ...f.session, id: "cs_other", client_reference_id: "other", metadata: { ...f.session.metadata, user_id: "other" } }], has_more: true })
    .mockResolvedValueOnce({ data: [], has_more: false });
  await f.worker().runOnce();
  await expect(f.service.create(f.intent)).rejects.toThrow("checkout_recovery_required");
  await f.worker().runOnce();
  expect(await f.state()).toMatchObject({ state: "verified", status: "absent" });
  const newAttempt = await createCheckoutRepository(payments).begin(f.intent, catalog, urls);
  expect(newAttempt).toMatchObject({ id: expect.any(String), user_id: f.userId, status: "creating" });
  expect((await admin.query("SELECT count(*)::text count FROM billing.checkout_attempts WHERE user_id=$1", [f.userId])).rows[0].count).toBe("1");
});

it("flags a missing completed session for review even when a complete list is empty", async () => {
  const other = await fixture({ status: "completed" }); await imports();
  other.listSessions.mockResolvedValue({ data: [], has_more: false });
  await other.worker().runOnce();
  expect(await other.state()).toMatchObject({ state: "review", last_recovery_error: "legacy_checkout_missing_session" });
});

it("rejects a foreign identity without releasing the account", async () => {
  const f = await fixture({ known: true }); await imports();
  f.session.metadata.license_id = "foreign-license";
  await f.worker().runOnce();
  expect(await f.state()).toMatchObject({ state: "review", last_recovery_error: "legacy_checkout_identity_mismatch" });
  expect(f.fetchSubscription).not.toHaveBeenCalled();
  await expect(f.service.create(f.intent)).rejects.toThrow("checkout_recovery_required");
});

it("serializes recovery across workers for one account", async () => {
  const f = await fixture({ known: true, future: true }); await imports();
  let active = 0, peak = 0;
  f.retrieveSession.mockImplementation(async () => {
    peak = Math.max(peak, ++active); await new Promise((resolve) => setTimeout(resolve, 25)); active--; return structuredClone(f.session);
  });
  expect((await Promise.all([f.worker().runOnce(), f.worker().runOnce()])).filter(Boolean)).toHaveLength(1);
  expect(peak).toBe(1);
  expect(await f.state()).toMatchObject({ state: "verified", status: "open" });
});

it("requeues a reviewed identity only through the same validation and exposes bounded review evidence", async () => {
  const f = await fixture({ known: true }); await imports();
  f.session.id = "cs_wrong_retrieval_id";
  await f.worker().runOnce();
  expect(await f.state()).toMatchObject({ state: "review", last_recovery_error: "legacy_checkout_identity_mismatch" });
  expect(await legacyCheckoutRecoveryReport(admin)).toMatchObject({ review: [{ id: f.attemptId, error: "legacy_checkout_identity_mismatch" }], next: null });
  expect(await legacyCheckoutRecoveryReport(admin, f.attemptId)).toMatchObject({ review: [] });
  expect(await retryLegacyCheckoutReview(admin, f.attemptId)).toEqual({ id: f.attemptId, requeued: true });
  expect(await retryLegacyCheckoutReview(admin, f.attemptId)).toEqual({ id: f.attemptId, requeued: false });
  await f.worker().runOnce();
  expect(await f.state()).toMatchObject({ state: "review" });
  f.session.id = f.sessionId;
  await retryLegacyCheckoutReview(admin, f.attemptId); await f.worker().runOnce();
  expect(await f.state()).toMatchObject({ state: "verified", status: "expired" });
  expect(f.createSession).not.toHaveBeenCalled();
});

it("rejects stalled pagination and an apparent session with a different original expiry", async () => {
  const f = await fixture(); await imports();
  f.listSessions.mockResolvedValueOnce({ data: [], has_more: true });
  await f.worker().runOnce();
  expect(await f.state()).toMatchObject({ state: "review", last_recovery_error: "legacy_checkout_invalid_pagination" });
  await retryLegacyCheckoutReview(admin, f.attemptId);
  f.session.expires_at--;
  await f.worker().runOnce();
  expect(await f.state()).toMatchObject({ state: "review", last_recovery_error: "legacy_checkout_window_mismatch" });
  await expect(f.service.create(f.intent)).rejects.toThrow("checkout_recovery_required");
});

it("rejects an import that would combine an unresolved native checkout with legacy attempts", async () => {
  const f = await fixture();
  await importLegacyPurchases(admin, { commit: true }); await importLegacySubscriptions(admin, { commit: true });
  await admin.query("INSERT INTO billing.accounts(user_id,license_id) VALUES($1,$2)", [f.userId, f.licenseId]);
  await admin.query(`INSERT INTO billing.checkout_attempts(id,user_id,license_id,tier,billing_interval,status,stripe_parameters,expires_at)
    VALUES($1,$2,$3,'pro','month','creating','{}',now()+INTERVAL '35 minutes')`, [randomUUID(), f.userId, f.licenseId]);
  await expect(importLegacyCheckouts(admin, { commit: true })).rejects.toThrow("conflicts with native data");
  expect((await admin.query("SELECT 1 FROM billing.legacy_checkout_records WHERE id=$1", [f.attemptId])).rowCount).toBe(0);
});
