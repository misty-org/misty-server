import { createHttpApp } from "../../../packages/runtime/src/http.js";
import type { Logger } from "../../../packages/runtime/src/logger.js";
import { createWebhookRoutes } from "./modules/webhooks/routes.js";
import { createBillingCommandRoutes } from "./modules/checkout/routes.js";
import { createBillingClosureRoutes } from "./modules/account-closure/routes.js";
import { createBillingSummaryRoutes } from "./modules/accounts/routes.js";

export function createPaymentsApp(options: {
  logger: Logger;
  webhooks: Parameters<typeof createWebhookRoutes>[0];
  webhookPath: string;
  checkDatabase: () => Promise<void>;
  isDraining: () => boolean;
  migrationComplete: boolean;
  commands?: Parameters<typeof createBillingCommandRoutes>[0];
  closure?: Parameters<typeof createBillingClosureRoutes>[0];
  summary?: Parameters<typeof createBillingSummaryRoutes>[0];
}) {
  const app = createHttpApp(options.logger);
  app.route(options.webhookPath, createWebhookRoutes(options.webhooks));
  if (options.summary) app.route("/internal/billing/summary", createBillingSummaryRoutes(options.summary));
  if (options.closure) app.route("/internal/billing/account-closure", createBillingClosureRoutes(options.closure));
  if (options.commands) app.route("/internal/billing", createBillingCommandRoutes(options.commands));
  app.get("/livez", (c) => c.json({ status: "ok" }));
  app.get("/readyz", async (c) => {
    c.header("Cache-Control", "no-store");
    if (options.isDraining()) return c.json({ status: "draining" }, 503);
    try { await options.checkDatabase(); }
    catch { return c.json({ status: "unavailable" }, 503); }
    if (!options.migrationComplete) return c.json({ status: "migrating" }, 503);
    return c.json({ status: "ok" });
  });
  return app;
}
