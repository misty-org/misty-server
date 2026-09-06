import { normalizePlan } from "../../../../../packages/entitlements/src/subscription-policy.js";
export { normalizePlan, subscriptionAccessDeadline, subscriptionLicenseState, type Plan } from "../../../../../packages/entitlements/src/subscription-policy.js";

export function entitlementsForTier(tier: string | null) {
  const plan = normalizePlan(tier);
  const limits = {
    basic: { storage: 2_000_000_000, spaces: 3, weeklyAi: 150_000n },
    pro: { storage: 50_000_000_000, spaces: 10, weeklyAi: 900_000n },
    max: { storage: 250_000_000_000, spaces: 10, weeklyAi: 1_800_000n },
  }[plan];
  return { plan, personalStorageBytes: limits.storage, spaceStorageBytes: limits.storage,
    maxOwnedSpaces: limits.spaces, personalWeeklyAi: limits.weeklyAi, spaceWeeklyAi: limits.weeklyAi };
}
