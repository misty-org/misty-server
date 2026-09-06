import type Stripe from "stripe";
import type { CheckoutIntent } from "../../../../../packages/service-contracts/src/payments.js";
import type { PriceCatalog } from "../subscriptions/model.js";

export type CheckoutErrorCode = "billing_account_closed" | "subscription_exists" | "checkout_in_progress" | "checkout_recovery_required" | "checkout_identity_mismatch" | "checkout_unavailable" | "portal_unavailable";
export class CheckoutError extends Error { constructor(readonly code: CheckoutErrorCode) { super(code); } }
export interface CheckoutUrls { success: string; cancel: string; portalReturn: string }
export function validateCheckoutUrls(urls: CheckoutUrls, allowInsecureLoopback = false): CheckoutUrls {
  for (const value of Object.values(urls)) {
    const url = new URL(value);
    const local = allowInsecureLoopback && url.protocol === "http:" && ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname);
    if ((!local && url.protocol !== "https:") || url.username || url.password) throw new Error("Checkout return URLs must use HTTPS");
  }
  return Object.freeze({ ...urls });
}

export function createCheckoutParameters(options: {
  intent: CheckoutIntent;
  attemptId: string;
  customerId: string | null;
  catalog: PriceCatalog;
  urls: CheckoutUrls;
  expiresAt: Date;
}): Stripe.Checkout.SessionCreateParams {
  const { intent } = options;
  const price = [...options.catalog.values()].find((candidate) => candidate.tier === intent.tier && candidate.interval === intent.interval);
  if (!price) throw new CheckoutError("checkout_unavailable");
  const metadata = {
    user_id: intent.userId, license_id: intent.licenseId, kind: "subscription",
    tier: intent.tier, interval: intent.interval, checkout_attempt_id: options.attemptId,
  };
  return {
    mode: "subscription", success_url: options.urls.success, cancel_url: options.urls.cancel,
    client_reference_id: intent.userId, metadata,
    subscription_data: { metadata, ...(intent.tier === "pro" && intent.trialEligible ? { trial_period_days: 14 } : {}) },
    ...(intent.tier === "pro" && intent.trialEligible ? { payment_method_collection: "always" as const } : {}),
    line_items: [{ price: price.id, quantity: 1 }], expires_at: Math.floor(options.expiresAt.getTime() / 1000),
    ...(options.customerId ? { customer: options.customerId } : { customer_email: intent.email }),
  };
}
