import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { z } from "zod";
import type { AuthService } from "../auth/service.js";
import { AuthBusy, SelfHostProofRequired, normalizeUsername, sessionTtlSeconds } from "../auth/model.js";
import { sessionToken, writeSessionCookie } from "../auth/cookies.js";
import type { RequestBoundary } from "../../../../../packages/runtime/src/request-boundary.js";
import { createSlidingWindowLimiter } from "../../../../../packages/runtime/src/rate-limit.js";
import { SelfHostError } from "./repository.js";
import type { SelfHostService } from "./service.js";

export function createSelfHostRoutes(options: { service: SelfHostService; auth: AuthService; boundary: RequestBoundary; deployment: "hosted" | "self_hosted"; now?: () => number }) {
  const app = new Hono();
  app.onError((error, c) => {
    if (error instanceof SelfHostProofRequired) return c.json({ code: "self_host_entitlement_required" }, 402);
    if (error instanceof AuthBusy) { c.header("Retry-After", "1"); return c.json({ code: "authentication_unavailable" }, 503); }
    if (error instanceof SelfHostError) {
      const status = { bootstrap_token_invalid: 410, enrollment_invitation_invalid: 410, entitlement_subject_already_enrolled: 409,
        account_already_exists: 409, admin_required: 403, enrollment_invitation_not_found: 404, entitlement_subject_mismatch: 403, self_host_entitlement_required: 402 } as const;
      return c.json({ code: error.code }, status[error.code]);
    }
    throw error;
  });
  app.get("/instance", async (c) => {
    c.header("Cache-Control", "no-store");
    try { return c.json(await options.service.instance()); } catch { return c.json({ code: "instance_unavailable" }, 503); }
  });
  const limit = createSlidingWindowLimiter({ limit: 20, windowMilliseconds: 60000, ...(options.now ? { now: options.now } : {}) });
  app.use("/self-host/*", async (c, next) => {
    c.header("Cache-Control", "no-store");
    if (options.deployment !== "self_hosted") return c.json({ code: "not_found" }, 404);
    const allowed = limit.allow(options.boundary.clientIp(c));
    if (!allowed.allowed) { c.header("Retry-After", String(allowed.retrySeconds)); return c.json({ code: "rate_limited" }, 429); }
    await next();
  });
  app.use("/self-host/*", bodyLimit({ maxSize: 8192, onError: (c) => c.json({ code: "invalid_request" }, 400) }));
  const account = z.object({ name: z.string().trim().min(1), username: z.string().trim().min(1), email: z.string().trim().min(1).transform((value) => value.toLowerCase()),
    password: z.string().refine((value) => Buffer.byteLength(value, "utf8") >= 8 && Buffer.byteLength(value, "utf8") <= 72),
    bootstrap_token: z.string().optional(), invitation: z.string().optional() }).strict();
  for (const kind of ["bootstrap", "enroll"] as const) app.post(`/self-host/${kind}`, async (c) => {
    const parsed = account.safeParse(await c.req.json().catch(() => undefined));
    if (!parsed.success) return c.json({ code: "invalid_request" }, 400);
    const credential = kind === "bootstrap" ? parsed.data.bootstrap_token : parsed.data.invitation;
    if (!credential?.trim()) return c.json({ code: "invalid_request" }, 400);
    let username: string;
    try { username = normalizeUsername(parsed.data.username); } catch { return c.json({ code: "invalid_username" }, 400); }
    const proof = c.req.header("X-Misty-Self-Hosted-Entitlement");
    const result = await options.service.createAccount({ ...parsed.data, username, credential, kind, ...(proof ? { proof } : {}) });
    writeSessionCookie(c, options.boundary, result.token, sessionTtlSeconds);
    return c.json(result, 201);
  });
  app.post("/self-host/invitations", async (c) => {
    const token = sessionToken(c), user = token ? await options.auth.authenticate(token) : null;
    if (!user) return c.json({ code: "not_authenticated" }, 401);
    return c.json(await options.service.invite(user.id), 201);
  });
  app.delete("/self-host/invitations/:invitationID", async (c) => {
    const token = sessionToken(c), user = token ? await options.auth.authenticate(token) : null;
    if (!user) return c.json({ code: "not_authenticated" }, 401);
    await options.service.revoke(user.id, c.req.param("invitationID"));
    return c.body(null, 204);
  });
  app.post("/self-host/entitlement", async (c) => {
    const token = sessionToken(c), user = token ? await options.auth.authenticate(token) : null;
    if (!user) return c.json({ code: "not_authenticated" }, 401);
    return c.json(await options.service.renew(user.id, c.req.header("X-Misty-Self-Hosted-Entitlement")));
  });
  return app;
}
