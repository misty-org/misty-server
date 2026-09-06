import type { PoolClient } from "pg";
import { entitlementsForTier } from "./policy.js";
export async function readEntitlements(tx: PoolClient, userId: string) {
  const row = (await tx.query<{ tier: string; status: string; expires_at: Date | null; legacy_tier: string | null; now: Date }>(
    "SELECT tier,status,expires_at,legacy_tier,now() AS now FROM licenses WHERE user_id=$1", [userId])).rows[0];
  if (!row) throw new Error("Account license is unavailable");
  return entitlementsForTier(row.status === "trialing" && row.expires_at && row.expires_at <= row.now ? row.legacy_tier : row.tier);
}
export function entitlementResponse(value: Awaited<ReturnType<typeof readEntitlements>>) {
  return { plan: value.plan, max_owned_spaces: value.maxOwnedSpaces, personal_storage_limit_bytes: value.personalStorageBytes,
    space_storage_limit_bytes: value.spaceStorageBytes, personal_ai_limit: Number(value.personalWeeklyAi), space_ai_limit: Number(value.spaceWeeklyAi),
    storage_limit_bytes: value.personalStorageBytes, space_limit: value.maxOwnedSpaces, unlimited_spaces: false, unlimited_collaborators: true, unlimited_agent_definitions: true };
}
