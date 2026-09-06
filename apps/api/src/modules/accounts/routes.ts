import { createSlidingWindowLimiter } from "../../../../../packages/runtime/src/rate-limit.js";
import { AccountExportUnavailable } from "./export-file.js";
import type { createAccountExport } from "./export.js";
import { AccountReauthenticationFailed } from "./reauthentication.js";
import { AuthBusy } from "../auth/model.js";
import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { z } from "zod";
import type { AuthService } from "../auth/service.js";
import { requireAccount, type AccountEnvironment } from "../auth/account-session.js";
import { AccountUnavailable, type createAccountRepository } from "./repository.js";
import { hashToken } from "../auth/service.js";
import { sessionToken } from "../auth/cookies.js";
import { BillingUnavailable } from "../billing/summary-client.js";
import type { AccountSummary } from "./summary.js";

// Go's boolean fields default to false when omitted/null; PUT replaces settings.
const preference = z.boolean().nullable().optional().transform((value) => value ?? false);
const settings = z.object({ email_updates_enabled: preference, analytics_enabled: preference, error_reporting_enabled: preference }).strict();
const telemetry = settings.omit({ email_updates_enabled: true });
const name = z.object({ name: z.string().nullable().optional().transform((value) => value?.trim() ?? "") }).strict();
const device = z.object({ device: z.string().nullable().optional().transform((value) => value ?? "") }).strict();

export function createAccountRoutes(options: { auth: AuthService; repository: ReturnType<typeof createAccountRepository>; summary?: AccountSummary; export?: ReturnType<typeof createAccountExport> }) {
  const app = new Hono<AccountEnvironment>(), repository = options.repository;
  app.onError((error, c) => { if (error instanceof AccountUnavailable) return c.json({ code: "not_authenticated" }, 401); if (error instanceof BillingUnavailable) return c.json({ code: "account_summary_unavailable" }, 503); if (error instanceof AccountReauthenticationFailed) return c.json({ code: "account_reauthentication_failed" }, 401); if (error instanceof AccountExportUnavailable) return c.json({ code: error.code }, 503); if (error instanceof AuthBusy) return c.json({ code: "account_export_unavailable" }, 503); throw error; });
  if (options.export) {
    const limiter = createSlidingWindowLimiter({ limit: 5, windowMilliseconds: 60000 });
    app.post("/me/export", requireAccount(options.auth), async (c, next) => {
      const allowed = limiter.allow(c.get("account").id);
      if (!allowed.allowed) { c.header("Retry-After", String(allowed.retrySeconds)); return c.text("too many requests\n", 429); }
      return next();
    }, bodyLimit({ maxSize: 4096, onError: c => c.json({ code: "invalid_request" }, 400) }), async c => {
      const body = z.object({ password: z.string().nullable().optional().transform(value => value ?? "") }).strict().safeParse(await c.req.json().catch(() => undefined));
      if (!body.success) return c.json({ code: "invalid_request" }, 400);
      return options.export!.manifest(c.get("account").id, hashToken(sessionToken(c)!), body.data.password, c.req.raw.signal);
    });
  }
  if (options.summary) {
    app.get("/me", requireAccount(options.auth), async (c) => c.json(await options.summary!.get(c.get("account").id, hashToken(sessionToken(c)!), c.req.raw.signal)));
  }
  for (const path of ["/me/profile", "/me/settings", "/me/telemetry", "/me/device"]) {
    app.use(path, requireAccount(options.auth));
    app.use(path, bodyLimit({ maxSize: 8192, onError: (c) => c.json({ code: "invalid_request" }, 400) }));
  }
  app.get("/me/settings", async (c) => c.json(await repository.settings(c.get("account").id)));
  app.put("/me/profile", async (c) => {
    const input = name.safeParse(await c.req.json().catch(() => undefined));
    if (!input.success) return c.json({ code: "invalid_request" }, 400);
    if (!input.data.name) return c.text("name is required\n", 400);
    await repository.name(c.get("account").id, input.data.name);
    return c.json({ status: "ok" });
  });
  app.put("/me/device", async (c) => {
    const input = device.safeParse(await c.req.json().catch(() => undefined));
    if (!input.success) return c.json({ code: "invalid_request" }, 400);
    await repository.device(c.get("account").id, input.data.device);
    return c.json({ status: "ok" });
  });
  app.put("/me/settings", async (c) => {
    const input = settings.safeParse(await c.req.json().catch(() => undefined));
    if (!input.success) return c.json({ code: "invalid_request" }, 400);
    await repository.settingsUpdate(c.get("account").id, input.data);
    return c.json({ status: "ok" });
  });
  app.put("/me/telemetry", async (c) => {
    const input = telemetry.safeParse(await c.req.json().catch(() => undefined));
    if (!input.success) return c.json({ code: "invalid_request" }, 400);
    await repository.telemetry(c.get("account").id, input.data.analytics_enabled, input.data.error_reporting_enabled);
    return c.json({ status: "ok" });
  });
  return app;
}
