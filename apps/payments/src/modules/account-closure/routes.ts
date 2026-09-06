import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import type { CryptoKey } from "jose";
import { createAdmission } from "../../../../../packages/runtime/src/admission.js";
import { verifyServiceAssertion } from "../../../../../packages/runtime/src/service-assertion.js";
import { billingClosureIntentSchema } from "../../../../../packages/service-contracts/src/payments.js";
import { BillingClosureConflict, BillingClosureUnavailable, type createBillingClosureRepository } from "./repository.js";

/** Private lifecycle command; main enables it only alongside active cleanup workers. */
export function createBillingClosureRoutes(options: { publicKeys: ReadonlyMap<string, CryptoKey>; repository: ReturnType<typeof createBillingClosureRepository> }) {
  const app = new Hono(), admission = createAdmission(4);
  app.use("*", async (c, next) => { c.header("Cache-Control", "no-store"); return admission.run(next, () => c.json({ code: "billing_closure_unavailable" }, 503)); });
  app.post("/", bodyLimit({ maxSize: 4096 }), async c => {
    const authorization = c.req.header("Authorization");
    if (!authorization?.startsWith("Bearer ")) return c.json({ code: "unauthorized" }, 401);
    const body = new Uint8Array(await c.req.arrayBuffer()); let subject: string;
    try { subject = (await verifyServiceAssertion({ token: authorization.slice(7), publicKeys: options.publicKeys,
      issuer: "misty-api", audience: "misty-payments", scope: "billing:account-closure", request: { method: c.req.method, path: c.req.path, body } })).subject; }
    catch { return c.json({ code: "unauthorized" }, 401); }
    let raw: unknown;
    try { raw = JSON.parse(new TextDecoder("utf8", { fatal: true }).decode(body)); } catch { return c.json({ code: "invalid_request" }, 400); }
    const input = billingClosureIntentSchema.safeParse(raw);
    if (!input.success) return c.json({ code: "invalid_request" }, 400);
    if (input.data.userId !== subject) return c.json({ code: "forbidden" }, 403);
    try { return c.json(await options.repository.begin(input.data)); }
    catch (error) {
      if (error instanceof BillingClosureConflict) return c.json({ code: "billing_closure_conflict" }, 409);
      if (error instanceof BillingClosureUnavailable) return c.json({ code: "billing_closure_unavailable" }, 503);
      throw error;
    }
  });
  return app;
}
