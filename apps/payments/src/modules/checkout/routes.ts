import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import type { CryptoKey } from "jose";
import { verifyServiceAssertion } from "../../../../../packages/runtime/src/service-assertion.js";
import { checkoutIntentSchema, portalIntentSchema } from "../../../../../packages/service-contracts/src/payments.js";
import { CheckoutError } from "./model.js";
import type { createCheckoutService } from "./service.js";
import type { createPortalService } from "./portal.js";

type CommandEnvironment = { Variables: { subject: string; assertionId: string; command: unknown } };
export function createBillingCommandRoutes(options: {
  publicKeys: ReadonlyMap<string, CryptoKey>;
  checkout: ReturnType<typeof createCheckoutService>;
  portal: ReturnType<typeof createPortalService>;
}) {
  const app = new Hono<CommandEnvironment>();
  app.use("*", bodyLimit({ maxSize: 16384 }));
  app.use("*", async (c, next) => {
    c.header("Cache-Control", "no-store");
    const action = c.req.path.split("/").at(-1);
    if (c.req.method !== "POST" || !["checkout", "portal"].includes(action ?? "")) return c.json({ code: "not_found" }, 404);
    const authorization = c.req.header("Authorization");
    if (!authorization?.startsWith("Bearer ")) return c.json({ code: "unauthorized" }, 401);
    const body = new Uint8Array(await c.req.arrayBuffer());
    try {
      const claims = await verifyServiceAssertion({
        token: authorization.slice(7), publicKeys: options.publicKeys,
        issuer: "misty-api", audience: "misty-payments", scope: `billing:${action}`,
        request: { method: c.req.method, path: c.req.path, body },
      });
      c.set("subject", claims.subject);
      c.set("assertionId", claims.requestId);
    } catch { return c.json({ code: "unauthorized" }, 401); }
    try { c.set("command", JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(body))); }
    catch { return c.json({ code: "invalid_request" }, 400); }
    return next();
  });
  app.post("/checkout", async (c) => {
    const command = checkoutIntentSchema.safeParse(c.get("command"));
    if (!command.success) return c.json({ code: "invalid_request" }, 400);
    if (command.data.userId !== c.get("subject")) return c.json({ code: "forbidden" }, 403);
    try { return c.json({ version: 1, userId: command.data.userId, licenseId: command.data.licenseId, url: await options.checkout.create(command.data) }); }
    catch (error) {
      if (error instanceof CheckoutError) return c.json({ code: error.code }, error.code === "checkout_unavailable" ? 503 : 409);
      throw error;
    }
  });
  app.post("/portal", async (c) => {
    const command = portalIntentSchema.safeParse(c.get("command"));
    if (!command.success) return c.json({ code: "invalid_request" }, 400);
    if (command.data.userId !== c.get("subject")) return c.json({ code: "forbidden" }, 403);
    try { return c.json({ version: 1, userId: command.data.userId, licenseId: command.data.licenseId, url: await options.portal.create(command.data.userId, command.data.licenseId, c.get("assertionId")) }); }
    catch (error) {
      if (error instanceof CheckoutError) return c.json({ code: error.code }, error.code === "checkout_unavailable" ? 503 : 409);
      throw error;
    }
  });
  return app;
}
