import { readFile } from "node:fs/promises";
import { importPKCS8 } from "jose";
import Stripe from "stripe";
import { createDatabasePool } from "../../../packages/database/src/pool.js";
import { assertRuntimeDatabaseRole } from "../../../packages/database/src/roles.js";
import { loadRuntimeConfig } from "../../../packages/runtime/src/config.js";
import { createLogger } from "../../../packages/runtime/src/logger.js";
import { startHttpServer } from "../../../packages/runtime/src/server.js";
import { startPaymentsWorkers } from "./workers.js";
import { loadServicePublicKeys } from "../../../packages/runtime/src/service-keys.js";
import { createPaymentsApp } from "./app.js";
import { loadPaymentsConfig } from "./config.js";
import { createWebhookRepository } from "./modules/webhooks/repository.js";
import { createWebhookWorker } from "./modules/webhooks/worker.js";
import { createEntitlementOutbox } from "./modules/entitlements/repository.js";
import { createEntitlementDispatcher } from "./modules/entitlements/dispatcher.js";
import { createSubscriptionReconciler } from "./modules/subscriptions/reconciler.js";
import { createLegacyCheckoutRecovery } from "./modules/legacy-checkout/recovery.js";
import { createCheckoutRecovery } from "./modules/checkout/recovery.js";
import { createCheckoutRepository } from "./modules/checkout/repository.js";
import { createCheckoutService } from "./modules/checkout/service.js";
import { createPortalService } from "./modules/checkout/portal.js";
import { createBillingClosureRepository } from "./modules/account-closure/repository.js";
import { createBillingClosureWorker } from "./modules/account-closure/worker.js";
import { createBillingSummaryRepository } from "./modules/accounts/summary.js";

const runtimeConfig = loadRuntimeConfig("payments", process.env);
if (runtimeConfig.deployment !== "hosted") throw new Error("The payments service is hosted-only");
const config = loadPaymentsConfig(process.env, runtimeConfig.environment !== "production");
const signingKey = await importPKCS8(await readFile(config.signingKeyFile, "utf8"), "EdDSA");
const apiPublicKeys = await loadServicePublicKeys(config.apiVerificationKeysFile);
const logger = createLogger(runtimeConfig);
const database = createDatabasePool(process.env, "payments");
database.on("error", (error) => logger.error({ errorType: error.name }, "idle database connection failed"));
const stripe = new Stripe(config.stripeSecretKey, { timeout: 8000, maxNetworkRetries: 0 });
const inbox = createWebhookRepository(database);
const outbox = createEntitlementOutbox(database);
const fetchSubscription = (id: string) => stripe.subscriptions.retrieve(id);
const checkout = createCheckoutService({ repository: createCheckoutRepository(database), catalog: config.catalog, urls: config.urls,
  gateway: {
    createSession: (parameters, idempotencyKey) => stripe.checkout.sessions.create(parameters, { idempotencyKey }),
    retrieveSession: (id) => stripe.checkout.sessions.retrieve(id),
    retrieveSubscription: fetchSubscription,
    cancelSubscription: (id) => stripe.subscriptions.cancel(id, { invoice_now: false, prorate: false }),
  },
});
const portal = createPortalService({ pool: database, returnUrl: config.urls.portalReturn,
  createSession: (customer, return_url, idempotencyKey) => stripe.billingPortal.sessions.create({ customer, return_url }, { idempotencyKey }),
});
const webhookWorker = createWebhookWorker({ inbox, catalog: config.catalog, fetchSubscription, fetchCharge: (id) => stripe.charges.retrieve(id) });
const reconciler = createSubscriptionReconciler({ pool: database, catalog: config.catalog, fetchSubscription });
const checkoutRecovery = createCheckoutRecovery({ pool: database, catalog: config.catalog, fetchSubscription,
  listSessions: (query) => stripe.checkout.sessions.list(query) });
const legacyCheckoutRecovery = createLegacyCheckoutRecovery({ pool: database, catalog: config.catalog, fetchSubscription,
  listSessions: (query) => stripe.checkout.sessions.list(query), retrieveSession: (id) => stripe.checkout.sessions.retrieve(id) });
const closure = createBillingClosureRepository(database);
const closureWorker = createBillingClosureWorker({ pool: database, catalog: config.catalog, gateway: {
  retrieveSession: id => stripe.checkout.sessions.retrieve(id), expireSession: id => stripe.checkout.sessions.expire(id),
  retrieveSubscription: fetchSubscription, cancelSubscription: id => stripe.subscriptions.cancel(id, { invoice_now: false, prorate: false }),
  retrieveCustomer: id => stripe.customers.retrieve(id), deleteCustomer: id => stripe.customers.del(id),
  listSessions: query => stripe.checkout.sessions.list(query), listSubscriptions: query => stripe.subscriptions.list(query),
} });
const dispatcher = createEntitlementDispatcher({
  endpoint: config.entitlementsEndpoint, privateKey: signingKey, keyId: config.signingKeyId, outbox,
  allowInsecureLoopback: runtimeConfig.environment !== "production",
});
let draining = false;
const app = createPaymentsApp({
  logger, webhooks: { stripe, inbox, signingSecret: config.webhookSecret }, webhookPath: config.webhookPath,
  ...(config.mode === "active" ? { commands: { publicKeys: apiPublicKeys, checkout, portal }, closure: { publicKeys: apiPublicKeys, repository: closure } } : {}),
  summary: { publicKeys: apiPublicKeys, repository: createBillingSummaryRepository(database) },
  checkDatabase: async () => {
    await assertRuntimeDatabaseRole(database, "payments");
    const query = { text: "SELECT 1 FROM billing.webhook_inbox LIMIT 1", query_timeout: 2000 };
    await database.query(query);
  },
  isDraining: () => draining,
  // Checkout, legacy reversals, backfill and application effects must pass before cutover.
  migrationComplete: false,
});
const workers = startPaymentsWorkers(config.mode, logger, {
  webhooks: isolated(webhookWorker.runOnce), reconciliation: isolated(reconciler.runOnce),
  checkoutRecovery: isolated(checkoutRecovery.runOnce), legacyCheckoutRecovery: isolated(legacyCheckoutRecovery.runOnce),
  delivery: isolated(dispatcher.runOnce), accountClosure: isolated(closureWorker.runOnce),
});
function isolated(operation: () => Promise<boolean>) {
  return async () => { await assertRuntimeDatabaseRole(database, "payments"); return operation(); };
}
const runtime = startHttpServer({
  app, config: runtimeConfig, logger, markDraining: () => { draining = true; },
  drain: async () => { await Promise.all(workers.map((worker) => worker.stop())); await database.end(); },
});
runtime.server.on("error", (error) => {
  logger.fatal({ errorType: error.name }, "payments server failed");
  process.exitCode = 1;
  void Promise.all(workers.map((worker) => worker.stop())).then(() => database.end());
});
for (const signal of ["SIGINT", "SIGTERM"] as const) {
  process.once(signal, () => {
    void runtime.close().catch((error: unknown) => {
      logger.error({ errorType: error instanceof Error ? error.name : "Unknown" }, "payments shutdown failed");
      process.exitCode = 1;
    });
  });
}
