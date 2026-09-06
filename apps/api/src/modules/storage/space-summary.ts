import type { PoolClient } from "pg";
import { clientInteger } from "../spaces/model.js";
import { readEntitlements } from "../entitlements/lookup.js";
import type { personalStorageSummary } from "./summary.js";

type Personal = Awaited<ReturnType<typeof personalStorageSummary>>["personal"];
/** Caller holds the Space row and storage advisory locks and checks permission.
 * Preserve Go's owner-only repair of the derived counter from authoritative
 * contribution/reservation ledgers before reading its version and balances.
 */
export async function spaceStorageSummary(tx: PoolClient, userId: string, spaceId: string, ownerId: string, personal: Personal) {
  await tx.query("INSERT INTO space_storage_usage(space_id) VALUES($1) ON CONFLICT DO NOTHING", [spaceId]);
  if (userId === ownerId) await tx.query(`WITH actual AS (SELECT
    COALESCE((SELECT sum(logical_bytes) FROM space_storage_contributions WHERE space_id=$1 AND state IN ('active','recovery')),0) AS used,
    COALESCE((SELECT sum(reserved_bytes) FROM space_upload_reservations WHERE space_id=$1 AND state='active'),0)
      +COALESCE((SELECT sum(reserved_bytes) FROM space_rendition_reservations WHERE space_id=$1 AND state='active'),0) AS reserved)
    UPDATE space_storage_usage u SET used_bytes=actual.used,reserved_bytes=actual.reserved,version=u.version+1,updated_at=now()
    FROM actual WHERE u.space_id=$1 AND (u.used_bytes<>actual.used OR u.reserved_bytes<>actual.reserved)`, [spaceId]);
  const row = (await tx.query<{ used_bytes: string; reserved_bytes: string; version: string }>("SELECT used_bytes,reserved_bytes,version FROM space_storage_usage WHERE space_id=$1", [spaceId])).rows[0]!;
  const used = BigInt(row.used_bytes), reserved = BigInt(row.reserved_bytes), limit = BigInt((await readEntitlements(tx, ownerId)).spaceStorageBytes);
  const space = { used_bytes: clientInteger(used), reserved_bytes: clientInteger(reserved), limit_bytes: clientInteger(limit),
    remaining_bytes: clientInteger(limit > used + reserved ? limit - used - reserved : 0n), over_quota: used + reserved > limit };
  return { space_id: spaceId, owner_user_id: ownerId,
    space_used_bytes: space.used_bytes, space_reserved_bytes: space.reserved_bytes, space_limit_bytes: space.limit_bytes,
    space_remaining_bytes: space.remaining_bytes, space_over_quota: space.over_quota,
    personal_used_bytes: personal.used_bytes, personal_reserved_bytes: personal.reserved_bytes, personal_limit_bytes: personal.limit_bytes,
    personal_remaining_bytes: personal.remaining_bytes, personal_over_quota: personal.over_quota, personal, space,
    used_bytes: personal.used_bytes, reserved_bytes: personal.reserved_bytes, limit_bytes: personal.limit_bytes,
    remaining_bytes: personal.remaining_bytes, version: clientInteger(row.version) };
}
