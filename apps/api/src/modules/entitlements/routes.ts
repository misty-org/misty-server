import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import type { CryptoKey } from "jose";
import { verifyServiceAssertion } from "../../../../../packages/runtime/src/service-assertion.js";
import { paymentEntitlementEventSchema } from "../../../../../packages/service-contracts/src/payments.js";
import { EntitlementAccountUnavailable, EntitlementConflict, type createEntitlementRepository } from "./repository.js";
import { LifetimeGrantAttributionPending, LifetimeGrantConflict } from "./service.js";

export function createEntitlementRoutes(options: {
  publicKeys: ReadonlyMap<string, CryptoKey>;
  repository: ReturnType<typeof createEntitlementRepository>;
}) {
  const app = new Hono();
  app.use("*", bodyLimit({ maxSize: 16384 }));
  app.post("/", async (c) => {
    c.header("Cache-Control", "no-store");
    const authorization = c.req.header("Authorization");
    if (!authorization?.startsWith("Bearer ")) return c.json({ code: "unauthorized" }, 401);
    const body = new Uint8Array(await c.req.arrayBuffer());
    let subject: string;
    try {
      ({ subject } = await verifyServiceAssertion({
        token: authorization.slice(7), publicKeys: options.publicKeys,
        issuer: "misty-payments", audience: "misty-api", scope: "entitlements:write",
        request: { method: c.req.method, path: c.req.path, body },
      }));
    } catch { return c.json({ code: "unauthorized" }, 401); }
    let payload: unknown;
    try { payload = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(body)); }
    catch { return c.json({ code: "invalid_request" }, 400); }
    const parsed = paymentEntitlementEventSchema.safeParse(payload);
    if (!parsed.success) return c.json({ code: "invalid_request" }, 400);
    if (parsed.data.userId !== subject) return c.json({ code: "forbidden" }, 403);
    try {
      const result = await options.repository.receive(parsed.data);
      return c.json({ eventId: parsed.data.eventId, result });
    } catch (error) {
      if (error instanceof EntitlementConflict) return c.json({ code: "entitlement_conflict" }, 409);
      if (error instanceof EntitlementAccountUnavailable) return c.json({ code: "account_unavailable" }, 409);
      if (error instanceof LifetimeGrantAttributionPending) return c.json({ code: "grant_attribution_pending" }, 409);
      if (error instanceof LifetimeGrantConflict) return c.json({ code: "grant_conflict" }, 409);
      throw error;
    }
  });
  return app;
}
