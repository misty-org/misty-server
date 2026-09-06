import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { z } from "zod";
import type { RequestBoundary } from "../../../../../../packages/runtime/src/request-boundary.js";
import { createSlidingWindowLimiter } from "../../../../../../packages/runtime/src/rate-limit.js";
import { sessionToken, writeSessionCookie } from "../cookies.js";
import { normalizeHandoffPath } from "./config.js";
import { handoffSessionSeconds, type HandoffService } from "./service.js";

export function createHandoffRoutes(options: { service: HandoffService; boundary: RequestBoundary; now?: () => number }) {
  const app = new Hono();
  const limiters = { mint: createSlidingWindowLimiter({ limit: 20, windowMilliseconds: 60000, ...(options.now ? { now: options.now } : {}) }),
    start: createSlidingWindowLimiter({ limit: 20, windowMilliseconds: 60000, ...(options.now ? { now: options.now } : {}) }) };
  app.use("*", async (c, next) => {
    c.header("Cache-Control", "no-store");
    c.header("Referrer-Policy", "no-referrer");
    const result = limiters[c.req.method === "GET" ? "start" : "mint"].allow(options.boundary.clientIp(c));
    if (!result.allowed) { c.header("Retry-After", String(result.retrySeconds)); return c.text("too many requests\n", 429); }
    await next();
  });
  app.post("/", bodyLimit({ maxSize: 8192, onError: (c) => c.json({ code: "invalid_request" }, 400) }), async (c) => {
    const token = sessionToken(c);
    if (!token) return c.text("not authenticated\n", 401);
    const raw = await c.req.text();
    let parsed: unknown = {};
    if (raw.trim()) { try { parsed = JSON.parse(raw); } catch { return c.json({ code: "invalid_request" }, 400); } }
    const input = z.object({ path: z.string().default("") }).strict().safeParse(parsed);
    if (!input.success) return c.json({ code: "invalid_request" }, 400);
    const path = normalizeHandoffPath(input.data.path);
    if (!path) return c.text("unsupported handoff path\n", 400);
    return c.json({ url: await options.service.mint(token, path) });
  });
  app.get("/start", async (c) => {
    const result = await options.service.start(c.req.query("token"));
    if (result.sessionToken) writeSessionCookie(c, options.boundary, result.sessionToken, handoffSessionSeconds);
    return c.redirect(result.location, 303);
  });
  return app;
}
