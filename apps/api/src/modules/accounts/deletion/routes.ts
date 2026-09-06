import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { z } from "zod";
import { createSlidingWindowLimiter } from "../../../../../../packages/runtime/src/rate-limit.js";
import type { RequestBoundary } from "../../../../../../packages/runtime/src/request-boundary.js";
import { requireAccount, type AccountEnvironment } from "../../auth/account-session.js";
import { sessionToken } from "../../auth/cookies.js";
import { AuthBusy } from "../../auth/model.js";
import { hashToken, type AuthService } from "../../auth/service.js";
import { AccountUnavailable } from "../repository.js";
import { AccountReauthenticationFailed } from "../reauthentication.js";
import { AccountDeletionAlreadyPending, AccountDeletionNotFound, AccountDeletionOwnership, AccountDeletionUnavailable } from "./model.js";
import type { createAccountDeletion } from "./repository.js";

export function createAccountDeletionRoutes(options: { auth: AuthService; boundary: RequestBoundary; repository: ReturnType<typeof createAccountDeletion> }) {
  const app = new Hono<AccountEnvironment>();
  const beginLimit = createSlidingWindowLimiter({ limit: 5, windowMilliseconds: 60000 });
  const statusLimit = createSlidingWindowLimiter({ limit: 60, windowMilliseconds: 60000 });
  const limitBody = bodyLimit({ maxSize: 4096, onError: c => c.json({ code: "invalid_request" }, 400) });
  app.use("*", async (c, next) => { c.header("Cache-Control", "no-store"); await next(); });
  app.onError((error, c) => {
    if (error instanceof AccountUnavailable) return c.json({ code: "not_authenticated" }, 401);
    if (error instanceof AccountReauthenticationFailed) return c.json({ code: "account_reauthentication_failed" }, 401);
    if (error instanceof AccountDeletionAlreadyPending) return c.json({ code: "account_deletion_already_pending" }, 409);
    if (error instanceof AccountDeletionNotFound) return c.json({ code: "account_deletion_not_found" }, 404);
    if (error instanceof AccountDeletionOwnership) return c.json({ code: "account_deletion_space_ownership", message: "Transfer or delete every Space you own before deleting your account.", spaces: error.spaces }, 409);
    if (error instanceof AccountDeletionUnavailable || error instanceof AuthBusy) return c.json({ code: "account_deletion_unavailable" }, 503);
    throw error;
  });
  app.post("/me/deletion", requireAccount(options.auth), async (c, next) => {
    const allowed = beginLimit.allow(c.get("account").id);
    if (!allowed.allowed) { c.header("Retry-After", String(allowed.retrySeconds)); return c.text("too many requests\n", 429); }
    return next();
  }, limitBody, async c => {
    const body = z.object({ password: z.string().nullable().optional().transform(value => value ?? ""), confirmation: z.string().nullable().optional() }).strict().safeParse(await c.req.json().catch(() => undefined));
    if (!body.success) return c.json({ code: "invalid_request" }, 400);
    if (body.data.confirmation !== "DELETE") return c.json({ code: "account_deletion_confirmation_required" }, 400);
    return c.json(await options.repository.begin(c.get("account").id, hashToken(sessionToken(c)!), body.data.password, c.req.raw.signal), 202);
  });
  app.post("/account/deletion/status", async (c, next) => {
    const allowed = statusLimit.allow(options.boundary.clientIp(c));
    if (!allowed.allowed) { c.header("Retry-After", String(allowed.retrySeconds)); return c.text("too many requests\n", 429); }
    return next();
  }, limitBody, async c => {
    const body = z.object({ request_id: z.string().max(200).nullable().optional().transform(value => value ?? ""), status_token: z.string().max(200).nullable().optional().transform(value => value ?? "") }).strict().safeParse(await c.req.json().catch(() => undefined));
    if (!body.success) return c.json({ code: "invalid_request" }, 400);
    return c.json(await options.repository.status(body.data.request_id, body.data.status_token, c.req.raw.signal));
  });
  return app;
}
