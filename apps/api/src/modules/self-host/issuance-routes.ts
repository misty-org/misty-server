import { Hono } from "hono";
import type { RequestBoundary } from "../../../../../packages/runtime/src/request-boundary.js";
import { createSlidingWindowLimiter } from "../../../../../packages/runtime/src/rate-limit.js";
import { sessionToken } from "../auth/cookies.js";
import type { AuthService } from "../auth/service.js";
import type { createSelfHostEligibility } from "./eligibility.js";
import type { createSelfHostIssuer } from "./issuer.js";

export function createSelfHostIssuanceRoutes(options: { auth: AuthService; boundary: RequestBoundary;
  eligibility: ReturnType<typeof createSelfHostEligibility>; issuer: ReturnType<typeof createSelfHostIssuer> | null; now?: () => Date }) {
  const app = new Hono(), clock = options.now ?? (() => new Date());
  const limit = createSlidingWindowLimiter({ limit: 20, windowMilliseconds: 60000, now: () => clock().getTime() });
  app.post("/billing/self-host-entitlement", async (c) => {
    c.header("Cache-Control", "no-store");
    const allowed = limit.allow(options.boundary.clientIp(c));
    if (!allowed.allowed) { c.header("Retry-After", String(allowed.retrySeconds)); return c.json({ code: "rate_limited" }, 429); }
    const token = sessionToken(c), user = token ? await options.auth.authenticate(token) : null;
    if (!user) return c.json({ code: "not_authenticated" }, 401);
    const now = clock(), deadline = await options.eligibility(user, now);
    if (!deadline) return c.json({ code: "self_host_entitlement_ineligible" }, 403);
    if (!options.issuer) return c.json({ code: "self_host_entitlement_unavailable" }, 503);
    try { return c.json(await options.issuer(user.id, deadline, now)); }
    catch { return c.json({ code: "self_host_entitlement_unavailable" }, 503); }
  });
  return app;
}
