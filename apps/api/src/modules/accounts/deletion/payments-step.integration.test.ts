import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Hono } from "hono";
import { generateKeyPair } from "jose";
import { Pool } from "pg";
import { afterAll, afterEach, beforeAll, expect, it, vi } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../../packages/database/src/test-database.js";
import { withTransaction } from "../../../../../../packages/database/src/transaction.js";
import { createBillingClosureRepository } from "../../../../../payments/src/modules/account-closure/repository.js";
import { createBillingClosureRoutes } from "../../../../../payments/src/modules/account-closure/routes.js";
import { createBillingClosureWorker } from "../../../../../payments/src/modules/account-closure/worker.js";
import type { ClosureGateway } from "../../../../../payments/src/modules/account-closure/model.js";
import { createPriceCatalog } from "../../../../../payments/src/modules/subscriptions/model.js";
import { createBillingClosureClient, type BillingClosureClient } from "../../billing/closure-client.js";
import { createAccountDeletionJobs } from "./jobs.js";
import { createDeletionPaymentsWorker } from "./payments-step.js";

const admin = createTestDatabase(), users: string[] = [];
const keys = await generateKeyPair("EdDSA", { crv: "Ed25519" });
let application: Pool, payments: Pool;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../payments/migrations/", import.meta.url))), "billing");
  await admin.query(`DO $$ BEGIN
    IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_deletion_bridge_api_test') THEN CREATE ROLE misty_deletion_bridge_api_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF;
    IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_deletion_bridge_payments_test') THEN CREATE ROLE misty_deletion_bridge_payments_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF;
    END $$;
    GRANT USAGE ON SCHEMA public TO misty_deletion_bridge_api_test;
    GRANT SELECT,UPDATE ON users,account_deletion_requests,account_deletion_steps TO misty_deletion_bridge_api_test;
    GRANT USAGE ON SCHEMA billing TO misty_deletion_bridge_payments_test;
    GRANT SELECT,INSERT,UPDATE ON billing.accounts,billing.account_closures,billing.account_closure_resources,billing.entitlement_versions,billing.entitlement_outbox TO misty_deletion_bridge_payments_test;
    GRANT SELECT,UPDATE ON billing.checkout_attempts TO misty_deletion_bridge_payments_test;
    GRANT SELECT ON billing.subscriptions,billing.legacy_purchases,billing.legacy_checkout_recovery,billing.cutover_checkpoints TO misty_deletion_bridge_payments_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_deletion_bridge_api_test", max: 4 });
  payments = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_deletion_bridge_payments_test", max: 3 });
}, 60000);
afterEach(async () => {
  for (const table of ["account_closures", "entitlement_outbox", "entitlement_versions", "accounts"]) await admin.query(`DELETE FROM billing.${table} WHERE user_id=ANY($1::text[])`, [users]);
  await admin.query("DELETE FROM billing.cutover_checkpoints WHERE name IN ('legacy_purchases_imported','legacy_subscriptions_imported','legacy_checkouts_imported')");
  await admin.query("DELETE FROM account_deletion_requests WHERE user_id=ANY($1::text[])", [users]);
  await admin.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]); users.length = 0;
});
afterAll(async () => { if (application) await application.end(); if (payments) await payments.end(); await admin.end(); });
async function fixture() {
  const userId = `bridge_${randomUUID().replaceAll("-", "").slice(0, 12)}`, licenseId = `license_${userId}`, requestId = `deletion_${randomUUID()}`; users.push(userId);
  await withTransaction(admin, async tx => {
    await tx.query("INSERT INTO users(id,email,password_hash,username,license_id,lifecycle_state) VALUES($1,$2,'test-only',$1,$3,'pending_deletion')", [userId, `${userId}@example.invalid`, licenseId]);
    await tx.query("INSERT INTO licenses(id,user_id) VALUES($1,$2)", [licenseId, userId]);
    await tx.query("INSERT INTO account_deletion_requests(id,user_id,status_token_hash,purge_after,cleanup_owner) VALUES($1,$2,$3,now()+interval '30 days','native')", [requestId, userId, randomUUID()]);
    await tx.query("INSERT INTO account_deletion_steps(request_id,step) SELECT $1,unnest(ARRAY['payments','providers','local','purge'])", [requestId]);
  });
  const intent = { version: 1 as const, userId, licenseId, deletionRequestId: requestId };
  const app = new Hono().route("/internal/billing/account-closure", createBillingClosureRoutes({ publicKeys: new Map([["api-key", keys.publicKey]]), repository: createBillingClosureRepository(payments) }));
  const send: typeof fetch = async (url, init) => app.fetch(new Request(url, init));
  const client = (fetcher: typeof fetch = send) => createBillingClosureClient({ endpoint: "https://payments.example.invalid/internal/billing", keyId: "api-key", privateKey: keys.privateKey, fetch: fetcher });
  const worker = (billing: BillingClosureClient = client()) => createDeletionPaymentsWorker({ pool: application, billing });
  const step = async () => (await admin.query("SELECT state,last_error_code,result,lease_token,completed_at,available_at,attempts FROM account_deletion_steps WHERE request_id=$1 AND step='payments'", [requestId])).rows[0];
  const due = () => admin.query("UPDATE account_deletion_steps SET available_at=clock_timestamp() WHERE request_id=$1 AND step='payments'", [requestId]);
  return { userId, licenseId, requestId, intent, client, send, worker, step, due };
}
it("follows the real signed service roundtrip and acknowledges only cleanup-worker verified closure", async () => {
  const f = await fixture(), worker = f.worker();
  expect(await worker.runOnce()).toBe(true);
  expect(await f.step()).toMatchObject({ state: "pending", completed_at: null, last_error_code: "", lease_token: null });
  expect((await f.step()).available_at.getTime()).toBeGreaterThan(Date.now());
  expect(await worker.runOnce()).toBe(false);
  expect((await admin.query("SELECT state FROM billing.account_closures WHERE user_id=$1", [f.userId])).rows[0].state).toBe("closing");
  await admin.query("INSERT INTO billing.cutover_checkpoints(name,evidence) VALUES('legacy_purchases_imported','{}'),('legacy_subscriptions_imported','{}'),('legacy_checkouts_imported','{}')");
  const unexpected = vi.fn(async (): Promise<never> => { throw new Error("An account with no billing resources must not call Stripe"); });
  const gateway: ClosureGateway = { retrieveSession: unexpected, expireSession: unexpected, retrieveSubscription: unexpected, cancelSubscription: unexpected,
    retrieveCustomer: unexpected, deleteCustomer: unexpected, listSessions: unexpected, listSubscriptions: unexpected };
  await createBillingClosureWorker({ pool: payments, catalog: createPriceCatalog([{ id: "price_pro_month", tier: "pro", interval: "month" }]), gateway }).runOnce();
  await f.due(); expect(await worker.runOnce()).toBe(true);
  expect(await f.step()).toMatchObject({ state: "completed", result: { outcome: "billing_closed" }, last_error_code: "", lease_token: null });
  expect((await f.step()).completed_at).toBeInstanceOf(Date); expect(unexpected).not.toHaveBeenCalled();
  expect(await createAccountDeletionJobs(application).claim("local")).toBeNull();
  expect((await admin.query("SELECT status FROM account_deletion_requests WHERE id=$1", [f.requestId])).rows[0].status).toBe("processing");
  await expect(application.query("SELECT * FROM billing.accounts")).rejects.toMatchObject({ code: "42501" });
  await expect(payments.query("SELECT * FROM users")).rejects.toMatchObject({ code: "42501" });
});
it("retries a lost committed closure receipt with the same request and no duplicate tombstone or entitlement", async () => {
  const f = await fixture(); let lost = true;
  const worker = f.worker(f.client(async (url, init) => { const result = await f.send(url, init); if (lost) { lost = false; await result.body?.cancel(); throw new Error("private upstream diagnostic"); } return result; }));
  await worker.runOnce(); expect(await f.step()).toMatchObject({ state: "pending", last_error_code: "payments_cleanup_unavailable", attempts: 1 });
  await f.due(); await worker.runOnce(); expect(await f.step()).toMatchObject({ state: "pending", last_error_code: "", attempts: 2 });
  expect((await admin.query("SELECT deletion_request_id FROM billing.account_closures WHERE user_id=$1", [f.userId])).rows).toEqual([{ deletion_request_id: f.requestId }]);
  expect((await admin.query("SELECT * FROM billing.entitlement_outbox WHERE user_id=$1", [f.userId])).rowCount).toBe(1);
  expect((await admin.query("SELECT last_error_code FROM account_deletion_requests WHERE id=$1", [f.requestId])).rows[0].last_error_code).toBe("");
});
it("preserves other-stage diagnostics during a valid closing poll", async () => {
  const f = await fixture();
  await admin.query("UPDATE account_deletion_requests SET last_error_code='providers_cleanup_unavailable' WHERE id=$1", [f.requestId]);
  await f.worker().runOnce();
  expect((await admin.query("SELECT last_error_code FROM account_deletion_requests WHERE id=$1", [f.requestId])).rows[0].last_error_code).toBe("providers_cleanup_unavailable");
  expect((await f.step()).last_error_code).toBe("");
});
it("does not hold API account or request locks across service I/O and rejects an obsolete worker after reclaim", async () => {
  const f = await fixture(); let entered!: () => void, release!: () => void;
  const started = new Promise<void>(resolve => { entered = resolve; }), wait = new Promise<void>(resolve => { release = resolve; });
  const pending = f.worker({ close: async () => { entered(); await wait; return { ...f.intent, state: "closing" }; } }).runOnce();
  try {
    await started;
    await withTransaction(admin, async tx => { await tx.query("SELECT id FROM users WHERE id=$1 FOR UPDATE NOWAIT", [f.userId]); await tx.query("SELECT id FROM account_deletion_requests WHERE id=$1 FOR UPDATE NOWAIT", [f.requestId]); });
    expect(await createAccountDeletionJobs(application).claim("payments")).toBeNull();
    await admin.query("UPDATE account_deletion_steps SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE request_id=$1 AND step='payments'", [f.requestId]);
    await f.worker({ close: async () => ({ ...f.intent, state: "closed" }) }).runOnce();
    expect((await f.step()).state).toBe("completed");
  } finally { release(); await pending; }
  expect(await f.step()).toMatchObject({ state: "completed", attempts: 2, result: { outcome: "billing_closed" } });
});
it("does not acknowledge an expired lease even when no replacement has claimed it", async () => {
  const f = await fixture();
  await f.worker({ close: async () => {
    await admin.query("UPDATE account_deletion_steps SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE request_id=$1 AND step='payments'", [f.requestId]);
    return { ...f.intent, state: "closed" };
  } }).runOnce();
  expect(await f.step()).toMatchObject({ state: "processing", completed_at: null, last_error_code: "" });
  await f.worker({ close: async () => ({ ...f.intent, state: "closed" }) }).runOnce();
  expect((await f.step()).state).toBe("completed");
});
it("refuses another deletion identity and records only an owned retry code", async () => {
  const f = await fixture();
  await f.worker({ close: async () => ({ ...f.intent, deletionRequestId: "private-other-request", state: "closed" }) }).runOnce();
  expect(await f.step()).toMatchObject({ state: "pending", last_error_code: "payments_cleanup_unavailable", result: {}, completed_at: null });
  expect(JSON.stringify(await f.step())).not.toContain("private-other-request");
});
it("rechecks account lifecycle and cleanup ownership after a remote closure completes", async () => {
  for (const change of ["lifecycle", "owner"]) {
    const f = await fixture();
    await f.worker({ close: async () => {
      if (change === "lifecycle") await admin.query("UPDATE users SET lifecycle_state='deleted' WHERE id=$1", [f.userId]);
      else await admin.query("UPDATE account_deletion_requests SET cleanup_owner='go' WHERE id=$1", [f.requestId]);
      return { ...f.intent, state: "closed" };
    } }).runOnce();
    expect(await f.step()).toMatchObject({ state: "processing", completed_at: null });
  }
});
