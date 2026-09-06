import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { z } from "zod";
import type { RequestBoundary } from "../../../../../packages/runtime/src/request-boundary.js";
import { createSlidingWindowLimiter } from "../../../../../packages/runtime/src/rate-limit.js";
import { AccountConflict, AuthBusy, AuthRejected, SelfHostProofRequired, SelfHostSubjectMismatch, normalizeUsername, sessionTtlSeconds } from "./model.js";
import type { AuthService } from "./service.js";
import { sessionToken, writeSessionCookie } from "./cookies.js";
import { createHandoffRoutes } from "./handoff/routes.js";
import type { HandoffService } from "./handoff/service.js";
import { createRecoveryRoutes } from "./recovery/routes.js";
import type { RecoveryService } from "./recovery/service.js";
export { sessionToken } from "./cookies.js";

const credentials = { email: z.string().trim().min(1).transform((value) => value.toLowerCase()), password: z.string().min(1) };
const loginBody = z.object(credentials).strict();
const registerBody = z.object({ ...credentials, name: z.string().default(""), username: z.string().min(1) }).strict();
export function createAuthRoutes(options: { service: AuthService; boundary: RequestBoundary; deployment: "hosted" | "self_hosted"; now?: () => number; handoff?: HandoffService; recovery?: RecoveryService }) {
  const app = new Hono();
  const loginLimit = createSlidingWindowLimiter({ limit: 20, windowMilliseconds: 60000, ...(options.now ? { now: options.now } : {}) });
  const registerLimit = createSlidingWindowLimiter({ limit: 10, windowMilliseconds: 60000, ...(options.now ? { now: options.now } : {}) });
  for (const path of ["/login", "/register", "/logout"]) {
    app.use(path, bodyLimit({ maxSize: 8192, onError: (c) => c.json({ code: "invalid_request" }, 400) }));
    app.use(path, async (c, next) => {
      c.header("Cache-Control", "no-store");
      const route = c.req.path.split("/").at(-1);
      const limiter = route === "login" ? loginLimit : route === "register" ? registerLimit : undefined;
      if (limiter) {
        const allowed = limiter.allow(options.boundary.clientIp(c));
        if (!allowed.allowed) { c.header("Retry-After", String(allowed.retrySeconds)); return c.text("too many requests\n", 429); }
      }
      await next();
    });
  }
  app.onError((error, c) => {
    if (error instanceof AuthBusy) { c.header("Retry-After", "1"); return c.text("authentication temporarily unavailable\n", 503); }
    if (error instanceof AuthRejected) return c.text("invalid credentials\n", 401);
    if (error instanceof AccountConflict) return c.text(error.message + "\n", 409);
    if (error instanceof SelfHostProofRequired) return c.json({ code: "self_host_entitlement_required" }, 402);
    if (error instanceof SelfHostSubjectMismatch) return c.json({ code: "entitlement_subject_mismatch" }, 403);
    throw error;
  });
  if (options.handoff) app.route("/auth/handoff", createHandoffRoutes({ service: options.handoff, boundary: options.boundary, ...(options.now ? { now: options.now } : {}) }));
  if (options.recovery) app.route("/auth", createRecoveryRoutes({ service: options.recovery, boundary: options.boundary, ...(options.now ? { now: options.now } : {}) }));
  app.post("/register", async (c) => {
    if (options.deployment !== "hosted") return c.json({ code: "self_host_registration_closed" }, 403);
    const body = registerBody.safeParse(await c.req.json().catch(() => undefined));
    if (!body.success) return c.json({ code: "invalid_request" }, 400);
    let username: string;
    try { username = normalizeUsername(body.data.username); }
    catch (error) { return c.text((error as Error).message + "\n", 400); }
    if (Buffer.byteLength(body.data.password, "utf8") > 72) return c.json({ code: "invalid_request" }, 400);
    const { user, token } = await options.service.register({ ...body.data, username, analyticsEnabled: c.req.header("X-Misty-Analytics-Enabled")?.trim().toLowerCase() === "true" });
    writeSessionCookie(c, options.boundary, token, sessionTtlSeconds);
    return c.json({ user_id: user.id, name: user.name, username: user.username, email: user.email, token }, 201);
  });
  app.post("/login", async (c) => {
    const body = loginBody.safeParse(await c.req.json().catch(() => undefined));
    if (!body.success) return c.json({ code: "invalid_request" }, 400);
    const proof = c.req.header("X-Misty-Self-Hosted-Entitlement");
    const { user, token } = await options.service.login({ ...body.data, ...(proof ? { selfHostProof: proof } : {}) });
    writeSessionCookie(c, options.boundary, token, sessionTtlSeconds);
    return c.json({ user_id: user.id, name: user.name, username: user.username, email: user.email, token });
  });
  app.post("/logout", async (c) => {
    await options.service.logout(sessionToken(c));
    writeSessionCookie(c, options.boundary, "", 0);
    return c.json({ status: "ok" });
  });
  return app;
}
