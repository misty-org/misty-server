import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import type { CryptoKey } from "jose";
import { verifyServiceAssertion } from "../../../../../packages/runtime/src/service-assertion.js";
import { billingSummaryIntentSchema } from "../../../../../packages/service-contracts/src/payments.js";
import { BillingIdentityConflict, BillingSummaryUnavailable, type createBillingSummaryRepository } from "./summary.js";

export function createBillingSummaryRoutes(options: { publicKeys: ReadonlyMap<string, CryptoKey>; repository: ReturnType<typeof createBillingSummaryRepository> }) {
  const app = new Hono();
  app.use("*", (c, next) => { c.header("Cache-Control", "no-store"); return next(); });
  app.post("/", bodyLimit({ maxSize: 4096 }), async (c) => {
    const authorization = c.req.header("Authorization");
    if (!authorization?.startsWith("Bearer ")) return c.json({ code: "unauthorized" }, 401);
    const body = new Uint8Array(await c.req.arrayBuffer());
    let subject: string;
    try {
      subject = (await verifyServiceAssertion({ token: authorization.slice(7), publicKeys: options.publicKeys,
        issuer: "misty-api", audience: "misty-payments", scope: "billing:summary",
        request: { method: c.req.method, path: c.req.path, body } })).subject;
    } catch { return c.json({ code: "unauthorized" }, 401); }
    let raw: unknown;
    try { raw = JSON.parse(new TextDecoder("utf8", { fatal: true }).decode(body)); }
    catch { return c.json({ code: "invalid_request" }, 400); }
    const input = billingSummaryIntentSchema.safeParse(raw);
    if (!input.success) return c.json({ code: "invalid_request" }, 400);
    if (input.data.userId !== subject) return c.json({ code: "forbidden" }, 403);
    try { return c.json(await options.repository.get(input.data)); }
    catch (error) {
      if (error instanceof BillingSummaryUnavailable) return c.json({ code: "billing_history_unavailable" }, 503);
      if (error instanceof BillingIdentityConflict) return c.json({ code: "billing_identity_conflict" }, 409);
      throw error;
    }
  });
  return app;
}
