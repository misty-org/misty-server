import { z } from "zod";

// Private service messages. This package must never be published with @misty/sdk.
const identity = z.string().min(1).max(256);
export const revisionSchema = z.string().regex(/^[1-9]\d{0,18}$/)
  .refine((value) => BigInt(value) <= 9223372036854775807n, "Revision exceeds PostgreSQL bigint");
export const subscriptionProjectionSchema = z.object({
  subscriptionId: identity,
  tier: z.enum(["pro", "max"]),
  interval: z.enum(["month", "year"]),
  status: z.enum(["incomplete", "incomplete_expired", "trialing", "active", "past_due", "canceled", "unpaid", "paused"]),
  currentPeriodEnd: z.iso.datetime().nullable(),
  cancelAtPeriodEnd: z.boolean(),
}).strict().refine((value) => !["active", "trialing"].includes(value.status) || value.currentPeriodEnd !== null,
  "Paid subscriptions require a billing period end");

const eventFields = {
  version: z.literal(1),
  eventId: z.uuid(),
  userId: identity,
  licenseId: identity,
  revision: revisionSchema,
  generatedAt: z.iso.datetime(),
};
export const subscriptionEntitlementEventSchema = z.object({
  ...eventFields, subscription: subscriptionProjectionSchema.nullable(),
}).strict();
export const purchaseReversalEventSchema = z.object({
  ...eventFields, kind: z.literal("purchase_reversal"), purchaseId: identity,
  reason: z.enum(["refunded", "disputed"]),
}).strict();
export const paymentEntitlementEventSchema = z.union([subscriptionEntitlementEventSchema, purchaseReversalEventSchema]);
export type PaymentEntitlementEvent = z.infer<typeof subscriptionEntitlementEventSchema>;
export type PurchaseReversalEvent = z.infer<typeof purchaseReversalEventSchema>;
export type PaymentEvent = z.infer<typeof paymentEntitlementEventSchema>;
export type SubscriptionProjection = z.infer<typeof subscriptionProjectionSchema>;

// Only the authenticated API supplies identity and local trial eligibility,
// serialized with its license writers. Payments also checks its own subscription
// and completed-purchase history under the billing account lock.
export const checkoutIntentSchema = z.object({
  version: z.literal(1), userId: identity, licenseId: identity,
  email: z.email().max(320), tier: z.enum(["pro", "max"]), interval: z.enum(["month", "year"]),
  trialEligible: z.boolean(),
}).strict();
export type CheckoutIntent = z.infer<typeof checkoutIntentSchema>;
export const portalIntentSchema = z.object({ version: z.literal(1), userId: identity, licenseId: identity }).strict();
export const billingSummaryIntentSchema = portalIntentSchema;
export const billingSummarySchema = z.object({
  version: z.literal(1), userId: identity, licenseId: identity,
  subscription: z.object({
    interval: z.enum(["month", "year"]),
    status: z.enum(["incomplete", "incomplete_expired", "trialing", "active", "past_due", "canceled", "unpaid", "paused"]),
    currentPeriodEnd: z.iso.datetime().nullable(), cancelAtPeriodEnd: z.boolean(), customerPortalAvailable: z.boolean(),
  }).strict().nullable(),
  hasCompletedPurchase: z.boolean(),
}).strict();
export type BillingSummaryIntent = z.infer<typeof billingSummaryIntentSchema>;
export type BillingSummary = z.infer<typeof billingSummarySchema>;

export const billingCommandResultSchema = z.object({
  version: z.literal(1), userId: identity, licenseId: identity,
  url: z.url().refine((value) => { const url = new URL(value); return url.protocol === "https:" && !url.username && !url.password; }),
}).strict();

// Private lifecycle command. The stable deletion ID, not a transient assertion
// ID, makes retries refer to exactly the same account closure.
export const billingClosureIntentSchema = z.object({ version: z.literal(1), userId: identity, licenseId: identity, deletionRequestId: identity }).strict();
export const billingClosureResultSchema = billingClosureIntentSchema.extend({ state: z.enum(["closing", "closed"]) });
export type BillingClosureIntent = z.infer<typeof billingClosureIntentSchema>;
export type BillingClosureResult = z.infer<typeof billingClosureResultSchema>;
