import type { PoolClient } from "pg";
import { entitlementsForTier } from "../entitlements/policy.js";

export class StorageQuotaExceeded extends Error {
  constructor(readonly dimension: "personal" | "space") { super(`${dimension}_storage_quota_exceeded`); }
}
/** Caller locks active Space/account and authenticates before entering storage. */
export async function lockStorage(tx: PoolClient, userId: string, spaceId: string) {
  // Same names/order as the temporary Go owner. All native storage mutations
  // take these locks before usage, upload/reservation and blob rows.
  for (const key of [`storage-personal:${userId}`, `storage-space:${spaceId}`].sort()) {
    await tx.query("SELECT pg_advisory_xact_lock(hashtext($1))", [key]);
  }
  await tx.query("INSERT INTO space_storage_usage(space_id) VALUES($1) ON CONFLICT DO NOTHING", [spaceId]);
  const row = (await tx.query<{ used_bytes: string; reserved_bytes: string }>(
    "SELECT used_bytes,reserved_bytes FROM space_storage_usage WHERE space_id=$1 FOR UPDATE", [spaceId])).rows[0]!;
  return { used: BigInt(row.used_bytes), reserved: BigInt(row.reserved_bytes) };
}
export async function checkStorageCapacity(tx: PoolClient, userId: string, spaceId: string, ownerId: string,
  space: { used: bigint; reserved: bigint }, bytes: bigint) {
  async function limit(id: string) {
    const license = (await tx.query<{ tier: string; status: string; expires_at: Date | null; legacy_tier: string | null; now: Date }>(
      "SELECT tier,status,expires_at,legacy_tier,now() AS now FROM licenses WHERE user_id=$1", [id])).rows[0];
    if (!license) throw new Error("Storage license is unavailable");
    const tier = license.status === "trialing" && license.expires_at && license.expires_at <= license.now ? license.legacy_tier : license.tier;
    return BigInt(entitlementsForTier(tier).personalStorageBytes);
  }
  const personal = (await tx.query<{ used: string; reserved: string }>(`SELECT
    COALESCE((SELECT sum(c.logical_bytes) FROM space_storage_contributions c JOIN spaces s ON s.id=c.space_id
      WHERE c.user_id=$1 AND c.state IN ('active','recovery') AND s.lifecycle_state='active'),0) AS used,
    COALESCE((SELECT sum(r.reserved_bytes) FROM space_upload_reservations r JOIN spaces s ON s.id=r.space_id
      WHERE r.user_id=$1 AND r.state='active' AND s.lifecycle_state='active'),0)
    + COALESCE((SELECT sum(r.reserved_bytes) FROM space_rendition_reservations r JOIN spaces s ON s.id=r.space_id
      WHERE r.user_id=$1 AND r.state='active' AND s.lifecycle_state='active'),0) AS reserved`, [userId])).rows[0]!;
  if (BigInt(personal.used) + BigInt(personal.reserved) + bytes > await limit(userId)) throw new StorageQuotaExceeded("personal");
  if (space.used + space.reserved + bytes > await limit(ownerId)) throw new StorageQuotaExceeded("space");
}
