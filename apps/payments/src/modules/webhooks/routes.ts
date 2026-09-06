import { createHash } from "node:crypto";
import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import type Stripe from "stripe";
import type { WebhookInbox } from "./repository.js";

export function createWebhookRoutes(options: {
  stripe: Pick<Stripe, "webhooks">;
  signingSecret: string;
  inbox: WebhookInbox;
}) {
  if (!options.signingSecret.startsWith("whsec_") || options.signingSecret.length < 16) {
    throw new Error("A valid Stripe webhook signing secret is required");
  }
  const app = new Hono();
  app.use("*", bodyLimit({ maxSize: 65536, onError: (c) => c.text("failed to read body", 400) }));
  app.post("/", async (c) => {
    const signature = c.req.header("Stripe-Signature");
    if (!signature) return c.text("invalid signature", 400);
    const raw = Buffer.from(await c.req.arrayBuffer());
    let event: Stripe.Event;
    try {
      event = options.stripe.webhooks.constructEvent(raw, signature, options.signingSecret);
    } catch {
      return c.text("invalid signature", 400);
    }
    try {
      await options.inbox.accept({
        id: event.id, type: event.type, created: event.created, payload: event,
        payloadSha256: createHash("sha256").update(raw).digest("hex"),
      });
    } catch {
      // Stripe must retry if the event was not durably recorded.
      return c.text("event processing failed", 500);
    }
    return c.body(null, 200);
  });
  return app;
}
