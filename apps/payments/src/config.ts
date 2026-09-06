import { z } from "zod";
import { paymentsModeSchema } from "./workers.js";
import type { Environment } from "../../../packages/runtime/src/config.js";
import { createPriceCatalog, type PriceDefinition } from "./modules/subscriptions/model.js";
import { validateCheckoutUrls } from "./modules/checkout/model.js";

const schema = z.object({
  mode: paymentsModeSchema,
  stripeSecretKey: z.string().regex(/^(sk|rk)_(test|live)_/),
  webhookSecret: z.string().startsWith("whsec_").min(16),
  webhookPath: z.string(),
  signingKeyFile: z.string().min(1), signingKeyId: z.string().min(1).max(100),
  entitlementsEndpoint: z.url(),
  apiVerificationKeysFile: z.string().min(1),
  checkoutSuccessUrl: z.url(), checkoutCancelUrl: z.url(), portalReturnUrl: z.url(),
});

export function parseWebhookPath(value: string | undefined): string {
  const raw = value?.trim() || "/stripe/webhook";
  const url = new URL(raw, "https://webhook-path.invalid");
  const local = ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname);
  if ((!raw.startsWith("/") && !/^https?:\/\//i.test(raw)) || raw.includes("\\") ||
      (!raw.startsWith("/") && url.protocol !== "https:" && !(url.protocol === "http:" && local)) ||
      raw.startsWith("//") || url.username || url.password || url.search || url.hash || url.pathname === "/" ||
      /[{}*:\\]/.test(decodeURIComponent(url.pathname)) ||
      ["/livez", "/readyz"].includes(url.pathname) || url.pathname.startsWith("/internal/")) {
    throw new Error("STRIPE_WEBHOOK_PATH must identify one static HTTPS webhook route");
  }
  return url.pathname;
}
export function loadPaymentsConfig(env: Environment, allowInsecureLoopback = false) {
  const result = schema.safeParse({
    mode: env.PAYMENTS_MODE ?? "paused",
    stripeSecretKey: env.STRIPE_SECRET_KEY, webhookSecret: env.STRIPE_WEBHOOK_SECRET,
    webhookPath: parseWebhookPath(env.STRIPE_WEBHOOK_PATH),
    signingKeyFile: env.PAYMENTS_SIGNING_KEY_FILE, signingKeyId: env.PAYMENTS_SIGNING_KEY_ID,
    entitlementsEndpoint: env.API_ENTITLEMENTS_URL,
    apiVerificationKeysFile: env.API_VERIFICATION_KEYS_FILE,
    checkoutSuccessUrl: env.STRIPE_CHECKOUT_SUCCESS_URL, checkoutCancelUrl: env.STRIPE_CHECKOUT_CANCEL_URL,
    portalReturnUrl: env.STRIPE_PORTAL_RETURN_URL,
  });
  if (!result.success) throw new Error(`Invalid payments configuration: ${result.error.issues.map((issue) => issue.path.join(".")).join(", ")}`);
  const prices: PriceDefinition[] = [
    { id: env.STRIPE_PRICE_PRO_MONTHLY ?? "", tier: "pro", interval: "month" },
    { id: env.STRIPE_PRICE_PRO_YEARLY ?? "", tier: "pro", interval: "year" },
    { id: env.STRIPE_PRICE_MAX_MONTHLY ?? "", tier: "max", interval: "month" },
    { id: env.STRIPE_PRICE_MAX_YEARLY ?? "", tier: "max", interval: "year" },
  ];
  return Object.freeze({ ...result.data, catalog: createPriceCatalog(prices),
    urls: validateCheckoutUrls({ success: result.data.checkoutSuccessUrl, cancel: result.data.checkoutCancelUrl, portalReturn: result.data.portalReturnUrl }, allowInsecureLoopback),
  });
}
