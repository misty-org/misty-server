import type Stripe from "stripe";
import { checkoutIntentSchema, type CheckoutIntent } from "../../../../../packages/service-contracts/src/payments.js";
import type { PriceCatalog } from "../subscriptions/model.js";
import { CheckoutError, type CheckoutUrls } from "./model.js";
import type { createCheckoutRepository } from "./repository.js";

type CheckoutSession = Pick<Stripe.Checkout.Session, "id" | "url" | "status" | "expires_at">;
export function createCheckoutService(options: {
  repository: ReturnType<typeof createCheckoutRepository>;
  catalog: PriceCatalog;
  urls: CheckoutUrls;
  gateway: {
    createSession: (parameters: Stripe.Checkout.SessionCreateParams, idempotencyKey: string) => Promise<CheckoutSession>;
    retrieveSession: (id: string) => Promise<CheckoutSession>;
    retrieveSubscription: (id: string) => Promise<unknown>;
    cancelSubscription: (id: string) => Promise<unknown>;
  };
}) {
  return {
    async create(intent: CheckoutIntent): Promise<string> {
      const input = checkoutIntentSchema.parse(intent);
      for (let retry = 0; retry < 2; retry++) {
        const attempt = await options.repository.begin(input, options.catalog, options.urls);
        if ("legacyUrl" in attempt) return attempt.legacyUrl;
        await options.repository.prepareReplacement(attempt, options.catalog, {
          retrieve: options.gateway.retrieveSubscription, cancel: options.gateway.cancelSubscription,
        });
        // Recheck admission and attempt state after replacement work, then keep
        // the account lock through Stripe and the result checkpoint. A lost
        // response rolls back this checkpoint but retains the original intent.
        const result = await options.repository.withOpenAttempt(attempt, async (current, actions) => {
          if (current.status === "completed") return { status: "completed" as const };
          if (["expired", "failed"].includes(current.status)) return { status: "expired" as const };
          if (current.status === "open" && current.checkout_url && current.expires_at.getTime() > Date.now()) return { status: "open" as const, url: current.checkout_url };
          let session: CheckoutSession;
          if (current.status === "open" && current.stripe_checkout_session_id) {
            session = await options.gateway.retrieveSession(current.stripe_checkout_session_id);
            if (session.id !== current.stripe_checkout_session_id) throw new CheckoutError("checkout_identity_mismatch");
            if (session.status !== "complete" && session.status !== "expired") throw new CheckoutError("checkout_in_progress");
          } else {
            // Stripe can prune keys after 24h; old unresolved intents require
            // canonical discovery, never a second create with a forgotten key.
            if (Date.now() - current.created_at.getTime() >= 23 * 60 * 60 * 1000) throw new CheckoutError("checkout_recovery_required");
            session = await options.gateway.createSession(current.stripe_parameters, `misty-subscription-checkout-${current.id}`);
          }
          if (!session.id) throw new CheckoutError("checkout_unavailable");
          if (session.status === "complete" || session.status === "expired") {
            const status = session.status === "complete" ? "completed" : "expired";
            await actions.resolveStatus(status, session.id); return { status };
          }
          if (!session.url || session.status !== "open" || session.expires_at * 1000 <= Date.now()) throw new CheckoutError("checkout_unavailable");
          const url = new URL(session.url);
          if (url.protocol !== "https:" || url.username || url.password) throw new CheckoutError("checkout_unavailable");
          await actions.open({ id: session.id, url: session.url, expiresAt: new Date(session.expires_at * 1000) });
          return { status: "open" as const, url: session.url };
        });
        if (result.status === "completed") throw new CheckoutError("subscription_exists");
        if (result.status === "open" && result.url) return result.url;
      }
      throw new CheckoutError("checkout_in_progress");
    },
  };
}
