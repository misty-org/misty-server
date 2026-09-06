import type { SubscriptionProjection } from "../../service-contracts/src/payments.js";

export type Plan = "basic" | "pro" | "max";
export function normalizePlan(tier: string | null): Plan {
  if (tier === "max") return "max";
  return tier === "pro" || tier === "personal" ? "pro" : "basic";
}

export function subscriptionAccessDeadline(subscription: SubscriptionProjection | null): Date | null {
  if (!subscription?.currentPeriodEnd) return null;
  const end = new Date(subscription.currentPeriodEnd);
  if (subscription.status === "trialing") return end;
  if (subscription.status === "active") return new Date(end.getTime() + 72 * 3600_000);
  return null;
}

export function subscriptionLicenseState(subscription: SubscriptionProjection | null, legacyTier: string | null, now: Date) {
  const end = subscription?.currentPeriodEnd ? new Date(subscription.currentPeriodEnd) : null;
  // Preserve the existing 72-hour missed-webhook/reconciliation fail-safe. A
  // trial itself expires at period end; it does not receive paid-access grace.
  if (subscription?.status === "trialing" && end && end > now) {
    return { tier: normalizePlan(subscription.tier), status: "trialing" as const, expiresAt: end };
  }
  const deadline = subscriptionAccessDeadline(subscription);
  if (subscription?.status === "active" && deadline && deadline > now) {
    return { tier: normalizePlan(subscription.tier), status: "active" as const, expiresAt: null };
  }
  return { tier: normalizePlan(legacyTier), status: "active" as const, expiresAt: null };
}
