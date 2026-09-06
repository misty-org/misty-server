import { z } from "zod";
import { subscriptionProjectionSchema } from "../../../../../packages/service-contracts/src/payments.js";

export type PriceDefinition = { id: string; tier: "pro" | "max"; interval: "month" | "year" };
export type PriceCatalog = ReadonlyMap<string, PriceDefinition>;
export function createPriceCatalog(definitions: readonly PriceDefinition[]): PriceCatalog {
  const catalog = new Map<string, PriceDefinition>();
  for (const definition of definitions) {
    if (!definition.id.startsWith("price_") || catalog.has(definition.id)) throw new Error("Stripe price catalog is invalid or ambiguous");
    catalog.set(definition.id, Object.freeze({ ...definition }));
  }
  return catalog;
}

const stripeId = z.union([z.string().min(1), z.object({ id: z.string().min(1) }).transform((value) => value.id)]);
const timestamp = z.number().int().nonnegative().max(253402300799);
const stripeSubscriptionSchema = z.object({
  id: z.string().startsWith("sub_"), customer: stripeId,
  metadata: z.object({ user_id: z.string().trim().min(1), license_id: z.string().trim().min(1),
    kind: z.literal("subscription"), tier: z.enum(["pro", "max"]), interval: z.enum(["month", "year"]) }),
  status: subscriptionProjectionSchema.shape.status,
  current_period_end: timestamp.nullish(),
  canceled_at: timestamp.nullish(), cancel_at_period_end: z.boolean(),
  items: z.object({ data: z.array(z.object({
    current_period_end: timestamp.nullish(),
    price: z.object({ id: z.string(), recurring: z.object({ interval: z.string() }).nullish() }),
  })).length(1) }),
});

export class SubscriptionValidationError extends Error {
  constructor(readonly code: "subscription_invalid" | "subscription_price_invalid" | "subscription_identity_mismatch") { super(code); }
}

export function normalizeSubscription(value: unknown, catalog: PriceCatalog) {
  const result = stripeSubscriptionSchema.safeParse(value);
  if (!result.success) throw new SubscriptionValidationError("subscription_invalid");
  const subscription = result.data;
  const item = subscription.items.data[0]!;
  const price = catalog.get(item.price.id);
  if (!price || price.tier !== subscription.metadata.tier || price.interval !== subscription.metadata.interval ||
      (item.price.recurring?.interval && item.price.recurring.interval !== price.interval)) {
    throw new SubscriptionValidationError("subscription_price_invalid");
  }
  const periodEnd = subscription.current_period_end || item.current_period_end || null;
  const projection = subscriptionProjectionSchema.safeParse({
    subscriptionId: subscription.id, tier: price.tier, interval: price.interval, status: subscription.status,
    currentPeriodEnd: periodEnd ? new Date(periodEnd * 1000).toISOString() : null,
    cancelAtPeriodEnd: subscription.cancel_at_period_end,
  });
  if (!projection.success) throw new SubscriptionValidationError("subscription_invalid");
  return {
    userId: subscription.metadata.user_id, licenseId: subscription.metadata.license_id,
    customerId: subscription.customer, priceId: price.id, projection: projection.data,
    canceledAt: subscription.canceled_at ? new Date(subscription.canceled_at * 1000).toISOString() : null,
  };
}

export function nextReconciliation(now: Date, periodEnd: string | null): Date {
  const sixHours = now.getTime() + 6 * 60 * 60 * 1000;
  if (!periodEnd) return new Date(sixHours);
  const nearEnd = new Date(periodEnd).getTime() + 15 * 60 * 1000;
  return new Date(Math.min(sixHours, nearEnd < now.getTime() ? now.getTime() + 15 * 60 * 1000 : nearEnd));
}
