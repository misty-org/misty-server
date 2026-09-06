import { Hono, type Context } from "hono";
import { bodyLimit } from "hono/body-limit";
import { getCookie, setCookie } from "hono/cookie";
import { z } from "zod";
import type { RequestBoundary } from "../../../../../../packages/runtime/src/request-boundary.js";
import { createSlidingWindowLimiter } from "../../../../../../packages/runtime/src/rate-limit.js";
import { hashToken } from "../service.js";
import { resetTtlSeconds, type RecoveryService } from "./service.js";

const cookieName = "misty_reset_token";
export function createRecoveryRoutes(options: { service: RecoveryService; boundary: RequestBoundary; now?: () => number }) {
  const app = new Hono();
  const limiter = (limit: number, windowMilliseconds = 60000) => createSlidingWindowLimiter({ limit, windowMilliseconds, ...(options.now ? { now: options.now } : {}) });
  const limits = { forgot: limiter(8), start: limiter(20), validate: limiter(20), reset: limiter(10) };
  const accountLimit = limiter(5, resetTtlSeconds * 1000);
  for (const path of ["/forgot", "/reset", "/reset/start", "/reset/validate"]) {
    app.use(path, async (c, next) => {
      c.header("Cache-Control", "no-store"); c.header("Referrer-Policy", "no-referrer");
      const name = c.req.path.split("/").at(-1)! as keyof typeof limits;
      const result = limits[name].allow(options.boundary.clientIp(c));
      if (!result.allowed) { c.header("Retry-After", String(result.retrySeconds)); return c.text("too many requests\n", 429); }
      await next();
    });
    app.use(path, bodyLimit({ maxSize: 8192, onError: (c) => c.json({ code: "invalid_request" }, 400) }));
  }
  const cookie = (c: Context, token: string, maxAge: number) => setCookie(c, cookieName, token, { path: "/", httpOnly: true,
    secure: options.boundary.secure(c), sameSite: "Lax", maxAge, expires: maxAge ? new Date((options.now?.() ?? Date.now()) + maxAge * 1000) : new Date(0) });
  app.post("/forgot", async (c) => {
    const parsed = z.object({ email: z.string().trim().min(1).max(1024).transform((value) => value.toLowerCase()) }).strict().safeParse(await c.req.json().catch(() => undefined));
    if (!parsed.success) return c.json({ code: "invalid_request" }, 400);
    const allowed = accountLimit.allow(hashToken(`${options.boundary.clientIp(c)}\0${parsed.data.email}`));
    if (!allowed.allowed) { c.header("Retry-After", String(allowed.retrySeconds)); return c.text("too many reset requests\n", 429); }
    await options.service.forgot(parsed.data.email);
    return c.json({ status: "ok", message: "If the account exists, a password reset email will be sent shortly." }, 202);
  });
  app.get("/reset/start", async (c) => {
    const token = c.req.query("token")?.trim();
    if (token) {
      const valid = await options.service.validate(token);
      cookie(c, valid ? token : "", valid ? resetTtlSeconds : 0);
    }
    return c.redirect(options.service.redirectUrl, 303);
  });
  app.get("/reset/validate", async (c) => {
    if (!await options.service.validate(getCookie(c, cookieName))) { cookie(c, "", 0); return c.text("invalid or expired reset token\n", 404); }
    return c.json({ status: "ok" });
  });
  app.post("/reset", async (c) => {
    const parsed = z.object({ new_password: z.string().min(1).refine((value) => Buffer.byteLength(value, "utf8") <= 72) }).strict().safeParse(await c.req.json().catch(() => undefined));
    if (!parsed.success) return c.json({ code: "invalid_request" }, 400);
    const valid = await options.service.reset(getCookie(c, cookieName), parsed.data.new_password);
    cookie(c, "", 0);
    return valid ? c.json({ status: "ok" }) : c.text("invalid or expired reset token\n", 400);
  });
  return app;
}
