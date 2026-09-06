import { randomUUID } from "node:crypto";
import { fileURLToPath } from "node:url";
import { once } from "node:events";
import type { Server } from "node:http";
import { serve } from "@hono/node-server";
import { generateKeyPair } from "jose";
import Stripe from "stripe";
import { Pool } from "pg";
import { pino } from "pino";
import { afterAll, afterEach, beforeAll, expect, it, vi } from "vitest";
import { applyMigrations, readMigrations } from "../../../../../packages/database/src/migrations.js";
import { createTestDatabase } from "../../../../../packages/database/src/test-database.js";
import { assertRuntimeDatabaseRole } from "../../../../../packages/database/src/roles.js";
import { createRequestBoundary } from "../../../../../packages/runtime/src/request-boundary.js";
import { createApi } from "../../app.js";
import { createPaymentsApp } from "../../../../payments/src/app.js";
import { createCheckoutRepository } from "../../../../payments/src/modules/checkout/repository.js";
import { createCheckoutService } from "../../../../payments/src/modules/checkout/service.js";
import { createPortalService } from "../../../../payments/src/modules/checkout/portal.js";
import { createPriceCatalog } from "../../../../payments/src/modules/subscriptions/model.js";
import { createWebhookRepository } from "../../../../payments/src/modules/webhooks/repository.js";
import { createAuthRepository } from "../auth/repository.js";
import { createAuthService, hashToken } from "../auth/service.js";
import { createPasswordHasher, type PasswordHasher } from "../auth/passwords.js";
import { createBillingCommandClient, type BillingCommands } from "./command-client.js";
import { createBillingCommands } from "./commands.js";

const admin = createTestDatabase(), users: string[] = [], servers: Server[] = [];
const keys = await generateKeyPair("EdDSA", { crv: "Ed25519" });
const catalog = createPriceCatalog([{ id: "price_pro", tier: "pro", interval: "month" }, { id: "price_max", tier: "max", interval: "month" }]);
const urls = { success: "https://website.example/success", cancel: "https://website.example/cancel", portalReturn: "https://website.example/billing" };
let application: Pool, payments: Pool, passwords: PasswordHasher;
beforeAll(async () => {
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../../internal/platform/postgres/migrations/", import.meta.url))));
  await applyMigrations(admin, await readMigrations(fileURLToPath(new URL("../../../../payments/migrations/", import.meta.url))), "billing");
  await admin.query(`DO $$ BEGIN
    IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_billing_commands_api_test') THEN CREATE ROLE misty_billing_commands_api_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF;
    IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_billing_commands_payments_test') THEN CREATE ROLE misty_billing_commands_payments_test NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF;
    END $$;
    GRANT USAGE ON SCHEMA public TO misty_billing_commands_api_test;
    GRANT SELECT,INSERT,UPDATE,DELETE ON users,licenses,sessions TO misty_billing_commands_api_test;
    GRANT USAGE ON SCHEMA billing TO misty_billing_commands_payments_test; GRANT SELECT ON billing.account_closures TO misty_billing_commands_payments_test;
    GRANT SELECT ON billing.cutover_checkpoints,billing.legacy_purchases,billing.legacy_checkout_recovery TO misty_billing_commands_payments_test;
    GRANT UPDATE ON billing.legacy_checkout_recovery TO misty_billing_commands_payments_test;
    GRANT SELECT,INSERT,UPDATE ON billing.accounts,billing.checkout_attempts,billing.subscriptions,billing.entitlement_versions,billing.entitlement_outbox TO misty_billing_commands_payments_test;`);
  application = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_billing_commands_api_test", max: 5 });
  payments = new Pool({ connectionString: process.env.MISTY_TEST_DATABASE_URL, options: "-c role=misty_billing_commands_payments_test", max: 5 });
  passwords = await createPasswordHasher();
}, 60000);
afterEach(async () => {
  for (const server of servers.splice(0)) { server.closeAllConnections(); await new Promise<void>((resolve) => server.close(() => resolve())); }
  for (const table of ["entitlement_outbox", "entitlement_versions", "subscriptions", "legacy_purchases", "checkout_attempts", "accounts"]) await admin.query(`DELETE FROM billing.${table} WHERE user_id=ANY($1::text[])`, [users]);
  await admin.query("DELETE FROM users WHERE id=ANY($1::text[])", [users]); users.length = 0;
  await admin.query("DELETE FROM billing.cutover_checkpoints WHERE name IN ('legacy_purchases_imported','legacy_subscriptions_imported','legacy_checkouts_imported')");
});
afterAll(async () => { if (application) await application.end(); if (payments) await payments.end(); await admin.end(); });
async function fixture() {
  const auth = createAuthService({ repository: createAuthRepository(application), passwords, deployment: "hosted" });
  const register = async () => {
    const name = `billing_${randomUUID().replaceAll("-", "").slice(0, 12)}`;
    const account = await auth.register({ username: name, email: `${name}@example.invalid`, name, password: "test-password", analyticsEnabled: false }); users.push(account.user.id);
    const licenseId = (await admin.query("SELECT license_id FROM users WHERE id=$1", [account.user.id])).rows[0].license_id as string;
    await admin.query("INSERT INTO billing.accounts(user_id,license_id) VALUES($1,$2)", [account.user.id, licenseId]);
    return { ...account, licenseId };
  };
  const account = await register();
  const sessions = new Map<string, { parameters: Stripe.Checkout.SessionCreateParams; id: string; url: string; status: "open"; expires_at: number }>();
  let afterCreate: (() => Promise<void>) | undefined;
  const createSession = vi.fn(async (parameters: Stripe.Checkout.SessionCreateParams, key: string) => {
    let value = sessions.get(key);
    if (!value) { const id = `cs_${randomUUID()}`; value = { parameters: structuredClone(parameters), id, url: `https://checkout.stripe.com/c/pay/${id}`, status: "open", expires_at: parameters.expires_at! }; sessions.set(key, value); }
    expect(parameters).toEqual(value.parameters);
    await afterCreate?.(); return value;
  });
  const portalSession = vi.fn(async (_customer: string, _returnUrl: string, _key: string) => ({ url: "https://billing.stripe.com/verified-portal" }));
  const checkout = createCheckoutService({ repository: createCheckoutRepository(payments), catalog, urls, gateway: {
    createSession, retrieveSession: async () => { throw new Error("Unexpected lookup"); },
    retrieveSubscription: async () => { throw new Error("Unexpected subscription retrieval"); }, cancelSubscription: async () => { throw new Error("Unexpected cancellation"); },
  } });
  const privateApp = createPaymentsApp({ logger: pino({ level: "silent" }), checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    webhookPath: "/stripe/webhook", webhooks: { stripe: new Stripe("sk_test_billing_commands_fixture"), inbox: createWebhookRepository(payments), signingSecret: "whsec_billing_commands_fixture" },
    commands: { publicKeys: new Map([["commands-key", keys.publicKey]]), checkout, portal: createPortalService({ pool: payments, returnUrl: urls.portalReturn, createSession: portalSession }) },
  });
  const server = serve({ fetch: privateApp.fetch, hostname: "127.0.0.1", port: 0 }) as Server; servers.push(server);
  if (!server.listening) await once(server, "listening");
  const client = createBillingCommandClient({ endpoint: `http://127.0.0.1:${(server.address() as { port: number }).port}/internal/billing`, keyId: "commands-key", privateKey: keys.privateKey, allowInsecureLoopback: true });
  const makeApi = (commands: BillingCommands | null = client, service = auth, deployment: "hosted" | "self_hosted" = "hosted") => createApi({ logger: pino({ level: "silent" }),
    checkDatabase: async () => {}, isDraining: () => false, migrationComplete: false,
    auth: { service, deployment, boundary: createRequestBoundary({ peerAddress: () => "127.0.0.1" }) },
    billing: { auth: service, commands: createBillingCommands({ pool: application, client: commands, deployment }) },
  });
  const api = makeApi();
  const post = (path = "/billing/checkout-session", body: unknown = { tier: "pro", interval: "month" }, token = account.token) => api.request(path, { method: "POST", headers: { Authorization: `Bearer ${token}` }, body: JSON.stringify(body) });
  const completeHistory = () => admin.query("INSERT INTO billing.cutover_checkpoints(name,evidence) VALUES('legacy_purchases_imported','{}'),('legacy_subscriptions_imported','{}'),('legacy_checkouts_imported','{}')");
  return { ...account, auth, register, client, makeApi, api, privateApp, post, createSession, portalSession, sessions, completeHistory, setAfterCreate: (callback?: () => Promise<void>) => { afterCreate = callback; } };
}

it("preserves account-only aliases, retired responses, cookie support and origin protection", async () => {
  const f = await fixture(); await f.completeHistory();
  await assertRuntimeDatabaseRole(application, "api"); await assertRuntimeDatabaseRole(payments, "payments");
  const appToken = `app_${randomUUID()}`;
  await admin.query("INSERT INTO user_app_installations(user_id,app_id,installed_version) VALUES($1,'journal','1.1.0')", [f.user.id]);
  await admin.query("INSERT INTO app_runtime_sessions(token_hash,user_id,app_id,expires_at) VALUES($1,$2,'journal',now()+INTERVAL '5 minutes')", [hashToken(appToken), f.user.id]);
  for (const prefix of ["", "/api", "/v1"]) {
    for (const path of ["checkout-session", "portal-session", "trial/start", "credit-checkout-session"]) expect((await f.post(`${prefix}/billing/${path}`, {}, appToken)).status).toBe(401);
    const trial = await f.post(`${prefix}/billing/trial/start`); expect(trial.status).toBe(410);
    expect(await trial.json()).toEqual({ code: "trial_checkout_required", message: "Start the 14-day Pro trial through checkout." });
    const retired = await f.post(`${prefix}/billing/credit-checkout-session`); expect(retired.status).toBe(410); expect(await retired.json()).toMatchObject({ code: "retired_product" });
    expect((await f.post(`${prefix}/billing/checkout-session`)).status).toBe(200);
  }
  const cookie = await f.api.request("/billing/checkout-session", { method: "POST", headers: { Cookie: `misty_session=${f.token}` }, body: '{"tier":"pro","interval":"month"}' }); expect(cookie.status).toBe(200);
  const hostile = await f.api.request("/billing/checkout-session", { method: "POST", headers: { Cookie: `misty_session=${f.token}`, Origin: "https://untrusted.invalid" }, body: '{}' }); expect(hostile.status).toBe(403);
  expect(f.sessions.size).toBe(1);
  expect([...f.sessions.values()][0]!.parameters.subscription_data?.trial_period_days).toBe(14);
  expect((await f.privateApp.request("/internal/billing/checkout", { method: "POST", headers: { Authorization: `Bearer ${f.token}` }, body: '{}' })).status).toBe(401);
});

it("validates input and import completeness before provider creation, and returns only the stored account's portal", async () => {
  const f = await fixture();
  expect((await f.post(undefined, { tier: "basic", interval: "month" })).status).toBe(400);
  expect((await f.post(undefined, { tier: "pro", interval: "forever" })).status).toBe(400);
  expect((await f.post(undefined, { tier: "pro", interval: "month", trialEligible: true, userId: "foreign" })).status).toBe(400);
  expect((await f.post()).status).toBe(409); expect(f.createSession).not.toHaveBeenCalled();
  await f.completeHistory();
  expect((await f.post("/billing/portal-session")).status).toBe(409);
  await admin.query("UPDATE billing.accounts SET stripe_customer_id='cus_verified_command' WHERE user_id=$1", [f.user.id]);
  const portal = await f.post("/billing/portal-session", { customer: "cus_foreign", return_url: "https://untrusted.invalid" });
  expect(portal.status).toBe(200); expect(await portal.json()).toEqual({ url: "https://billing.stripe.com/verified-portal" });
  expect(f.portalSession.mock.calls[0]?.slice(0, 2)).toEqual(["cus_verified_command", urls.portalReturn]);
  expect(portal.headers.get("Cache-Control")).toBe("no-store");
});

it.each(["local-trial", "lifetime", "canceled", "purchase", "max"])("does not offer a new Pro trial for %s eligibility", async (reason) => {
  const f = await fixture(); await f.completeHistory();
  if (reason === "local-trial") await admin.query("UPDATE licenses SET trial_started_at=now() WHERE id=$1", [f.licenseId]);
  if (reason === "lifetime") await admin.query("UPDATE licenses SET legacy_tier='max' WHERE id=$1", [f.licenseId]);
  if (reason === "canceled") await admin.query(`INSERT INTO billing.subscriptions(stripe_subscription_id,user_id,stripe_customer_id,stripe_price_id,tier,billing_interval,status)
    VALUES($1,$2,'cus_historical','price_pro','pro','month','canceled')`, [`sub_${randomUUID()}`, f.user.id]);
  if (reason === "purchase") await admin.query(`INSERT INTO billing.legacy_purchases(id,user_id,license_id,tier_purchased,stripe_checkout_session_id,status,created_at,updated_at)
    VALUES($1,$2,$3,'personal',$4,'completed',now(),now())`, [randomUUID(), f.user.id, f.licenseId, `cs_${randomUUID()}`]);
  expect((await f.post(undefined, { tier: reason === "max" ? "max" : "pro", interval: "month" })).status).toBe(200);
  expect([...f.sessions.values()][0]!.parameters.subscription_data?.trial_period_days).toBeUndefined();
});

it("preserves one persisted request after a lost provider response and does not automatically replay the HTTP mutation", async () => {
  const f = await fixture(); await f.completeHistory();
  f.setAfterCreate(async () => { f.setAfterCreate(); throw new Error("Provider response lost after creation"); });
  expect((await f.post()).status).toBe(503); expect(f.createSession).toHaveBeenCalledTimes(1);
  await admin.query("UPDATE licenses SET trial_started_at=now() WHERE id=$1", [f.licenseId]);
  expect((await f.post()).status).toBe(200);
  expect(f.createSession).toHaveBeenCalledTimes(2); expect(f.sessions.size).toBe(1);
  expect(f.createSession.mock.calls[0]![1]).toBe(f.createSession.mock.calls[1]![1]);
});

it("holds license/session locks through provider creation and rejects a revoked exact session before sending", async () => {
  const f = await fixture(); await f.completeHistory();
  let entered!: () => void, release!: () => void; const ready = new Promise<void>((resolve) => { entered = resolve; }); const gate = new Promise<void>((resolve) => { release = resolve; });
  f.setAfterCreate(async () => { entered(); await gate; });
  const pending = f.post();
  try {
    await Promise.race([ready, Promise.resolve(pending).then((response) => { throw new Error(`Checkout ended before provider gate: ${response.status}`); })]);
    await expect(admin.query("SELECT id FROM licenses WHERE id=$1 FOR UPDATE NOWAIT", [f.licenseId])).rejects.toMatchObject({ code: "55P03" });
    await expect(admin.query("SELECT token_hash FROM sessions WHERE token_hash=$1 FOR UPDATE NOWAIT", [hashToken(f.token)])).rejects.toMatchObject({ code: "55P03" });
  } finally { release(); }
  expect((await pending).status).toBe(200);
  f.setAfterCreate();
  const staleAuth = { ...f.auth, authenticate: async (token: string) => { const user = await f.auth.authenticate(token); await f.auth.logout(token); return user; } };
  const denied = await f.makeApi(f.client, staleAuth).request("/billing/portal-session", { method: "POST", headers: { Authorization: `Bearer ${f.token}` } });
  expect(denied.status).toBe(401); expect(f.portalSession).not.toHaveBeenCalled();
});

it("bounds concurrent mutations across aliases and shares account rate limits", async () => {
  const f = await fixture(); await f.completeHistory(); const second = await f.register();
  let entered = 0, ready!: () => void, release!: () => void; const started = new Promise<void>((resolve) => { ready = resolve; }); const gate = new Promise<void>((resolve) => { release = resolve; });
  f.setAfterCreate(async () => { if (++entered === 2) ready(); await gate; });
  const pending = [f.post(), f.post("/v1/billing/checkout-session", undefined, second.token)];
  try { await Promise.race([started, Promise.all(pending).then((responses) => { throw new Error(`Checkouts ended before admission gate: ${responses.map((r) => r.status)}`); })]); expect((await f.post("/api/billing/checkout-session")).status).toBe(503); }
  finally { release(); }
  expect((await Promise.all(pending)).map((response) => response.status)).toEqual([200, 200]);
  for (let n = 0; n < 10; n++) expect((await f.post(`${n % 2 ? "/v1" : "/api"}/billing/trial/start`)).status).toBe(410);
  const limited = await f.post("/billing/trial/start"); expect(limited.status).toBe(429); expect(limited.headers.get("Retry-After")).toBeTruthy();
});

it("withholds a returned URL after wall-clock session expiry and disables billing for self-host", async () => {
  const f = await fixture(); await f.completeHistory();
  await admin.query("UPDATE sessions SET expires_at=now()+INTERVAL '1 second' WHERE token_hash=$1", [hashToken(f.token)]);
  f.setAfterCreate(async () => { await new Promise((resolve) => setTimeout(resolve, 1100)); });
  expect((await f.post()).status).toBe(401); expect(f.sessions.size).toBe(1);
  const selfHost = await f.makeApi(null, f.auth, "self_hosted").request("/billing/checkout-session", { method: "POST", headers: { Authorization: `Bearer ${f.token}` }, body: '{}' });
  expect(selfHost.status).toBe(501);
});
