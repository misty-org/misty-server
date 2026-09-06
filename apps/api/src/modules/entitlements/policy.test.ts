import { expect, it } from "vitest";
import type { SubscriptionProjection } from "../../../../../packages/service-contracts/src/payments.js";
import { entitlementsForTier, subscriptionLicenseState } from "./policy.js";
import { nextWeeklyReset } from "../usage/wallet.js";

const now = new Date("2026-09-07T00:00:00Z");
const subscription: SubscriptionProjection = { subscriptionId: "sub_test", tier: "pro", interval: "month", status: "active",
  currentPeriodEnd: "2026-09-08T00:00:00Z", cancelAtPeriodEnd: true };
it("preserves plan storage, ownership and weekly AI limits including historical personal tiers", () => {
  expect(entitlementsForTier("personal")).toEqual(entitlementsForTier("pro"));
  expect(entitlementsForTier("basic")).toMatchObject({ maxOwnedSpaces: 3, personalStorageBytes: 2_000_000_000, personalWeeklyAi: 150_000n });
  expect(entitlementsForTier("pro")).toMatchObject({ maxOwnedSpaces: 10, personalStorageBytes: 50_000_000_000, personalWeeklyAi: 900_000n });
  expect(entitlementsForTier("max")).toMatchObject({ maxOwnedSpaces: 10, personalStorageBytes: 250_000_000_000, personalWeeklyAi: 1_800_000n });
});
it("keeps canceled-at-period-end subscriptions active and falls back to lifetime access after actual revocation", () => {
  expect(subscriptionLicenseState(subscription, "max", now)).toMatchObject({ tier: "pro", status: "active" });
  for (const status of ["canceled", "past_due", "unpaid", "paused", "incomplete", "incomplete_expired"] as const) {
    expect(subscriptionLicenseState({ ...subscription, status }, "max", now)).toEqual({ tier: "max", status: "active", expiresAt: null });
  }
});
it("expires trials at their deadline and active subscriptions at the 72-hour fail-safe", () => {
  expect(subscriptionLicenseState({ ...subscription, status: "trialing", currentPeriodEnd: now.toISOString() }, null, now).tier).toBe("basic");
  expect(subscriptionLicenseState({ ...subscription, currentPeriodEnd: new Date(now.getTime() - 72 * 3600_000 + 1).toISOString() }, null, now).tier).toBe("pro");
  expect(subscriptionLicenseState({ ...subscription, currentPeriodEnd: new Date(now.getTime() - 72 * 3600_000).toISOString() }, "max", now).tier).toBe("max");
});
it("resets on the next UTC Monday, including exact Monday and year boundaries", () => {
  expect(nextWeeklyReset(now).toISOString()).toBe("2026-09-14T00:00:00.000Z");
  expect(nextWeeklyReset(new Date("2026-09-06T23:59:59.999Z")).toISOString()).toBe(now.toISOString());
  expect(nextWeeklyReset(new Date("2026-12-31T12:00:00-08:00")).toISOString()).toBe("2027-01-04T00:00:00.000Z");
});
