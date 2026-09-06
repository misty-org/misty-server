import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { z } from "zod";
import { createSlidingWindowLimiter } from "../../../../../packages/runtime/src/rate-limit.js";
import { createAdmission } from "../../../../../packages/runtime/src/admission.js";
import { requireAccount, type AccountEnvironment } from "../auth/account-session.js";
import type { AuthService } from "../auth/service.js";
import { hashToken } from "../auth/service.js";
import { sessionToken } from "../auth/cookies.js";
import { AccountUnavailable } from "../accounts/repository.js";
import { BillingUnavailable } from "./summary-client.js";
import { BillingCommandConflict } from "./command-client.js";
import { BillingUsagePermissionDenied, type createBillingUsage } from "./usage.js";
import type { createBillingCommands } from "./commands.js";

const request = z.object({ tier: z.string().nullable().optional(), interval: z.string().nullable().optional() }).strict();
export function createBillingRoutes(options: { auth: AuthService; commands: ReturnType<typeof createBillingCommands>; usage?: ReturnType<typeof createBillingUsage> }) {
  const app = new Hono<AccountEnvironment>(), admission = createAdmission(2);
  app.onError((error, c) => {
    if (error instanceof BillingUsagePermissionDenied) return c.text("internal error\n", 500);
    if (error instanceof AccountUnavailable) return c.json({ code: "not_authenticated" }, 401);
    if (error instanceof BillingUnavailable) return c.json({ code: "billing_unavailable" }, 503);
    if (error instanceof BillingCommandConflict) {
      const messages = { billing_account_closed: "account billing is closed", subscription_exists: "active subscription already exists", checkout_in_progress: "subscription checkout already in progress",
        checkout_recovery_required: "subscription checkout requires recovery", checkout_identity_mismatch: "subscription checkout identity conflicts",
        checkout_unavailable: "subscription checkout unavailable", portal_unavailable: "customer portal unavailable" };
      return c.text(`${messages[error.code]}\n`, error.code === "checkout_unavailable" ? 503 : 409);
    }
    throw error;
  });
  // Exact paths avoid intercepting the separate self-host proof issuer.
  for (const path of ["/billing/checkout-session", "/billing/portal-session", "/billing/trial/start", "/billing/credit-checkout-session"]) {
    const limiter = createSlidingWindowLimiter({ limit: path === "/billing/portal-session" ? 20 : 10, windowMilliseconds: 60000 });
    app.use(path, requireAccount(options.auth));
    app.use(path, async (c, next) => {
      const allowed = limiter.allow(c.get("account").id);
      if (!allowed.allowed) { c.header("Retry-After", String(allowed.retrySeconds)); return c.text("too many requests\n", 429); }
      return next();
    });
  }
  for (const path of ["/billing/checkout-session", "/billing/portal-session"]) {
    app.use(path, (c, next) => admission.run(next, () => c.json({ code: "billing_unavailable" }, 503)));
    app.use(path, bodyLimit({ maxSize: 4096, onError: (c) => c.json({ code: "invalid_request" }, 400) }));
  }
  if (options.usage) app.get("/billing/usage", requireAccount(options.auth), (c) => admission.run(async () =>
    c.json(await options.usage!.get(c.get("account").id, hashToken(sessionToken(c)!), c.req.raw.signal)),
    () => c.json({ code: "billing_unavailable" }, 503)));
  app.post("/billing/checkout-session", async (c) => {
    const body = request.safeParse(await c.req.json().catch(() => undefined));
    if (!body.success) return c.json({ code: "invalid_request" }, 400);
    if (body.data.tier !== "pro" && body.data.tier !== "max") return c.text("invalid tier\n", 400);
    if (body.data.interval !== "month" && body.data.interval !== "year") return c.text("invalid billing interval\n", 400);
    return c.json(await options.commands.checkout(c.get("account").id, hashToken(sessionToken(c)!), { tier: body.data.tier, interval: body.data.interval }, c.req.raw.signal));
  });
  app.post("/billing/portal-session", async (c) => c.json(await options.commands.portal(c.get("account").id, hashToken(sessionToken(c)!), c.req.raw.signal)));
  app.post("/billing/trial/start", (c) => c.json({ code: "trial_checkout_required", message: "Start the 14-day Pro trial through checkout." }, 410));
  app.post("/billing/credit-checkout-session", (c) => c.json({ code: "retired_product", message: "AI agent usage add-ons are no longer sold." }, 410));
  return app;
}
