import type { PoolClient } from "pg";
import { readEntitlements } from "../entitlements/lookup.js";

function integer(value: bigint) { const number = Number(value); if (!Number.isSafeInteger(number)) throw new Error("Storage total exceeds supported client precision"); return number; }
export async function personalStorageSummary(tx: PoolClient, userId: string) {
  const rows = (await tx.query<{ space_id: string; name: string; used_bytes: string; reserved_bytes: string }>(`WITH contributions AS (
    SELECT space_id,logical_bytes AS used_bytes,0::bigint AS reserved_bytes FROM space_storage_contributions WHERE user_id=$1 AND state IN ('active','recovery')
    UNION ALL SELECT space_id,0,reserved_bytes FROM space_upload_reservations WHERE user_id=$1 AND state='active'
    UNION ALL SELECT space_id,0,reserved_bytes FROM space_rendition_reservations WHERE user_id=$1 AND state='active')
    SELECT s.id AS space_id,s.name,sum(c.used_bytes) AS used_bytes,sum(c.reserved_bytes) AS reserved_bytes FROM contributions c
    JOIN spaces s ON s.id=c.space_id AND s.lifecycle_state='active' GROUP BY s.id,s.name,s.created_at ORDER BY s.created_at,s.id`, [userId])).rows;
  const used = rows.reduce((sum, row) => sum + BigInt(row.used_bytes), 0n), reserved = rows.reduce((sum, row) => sum + BigInt(row.reserved_bytes), 0n);
  const limit = BigInt((await readEntitlements(tx, userId)).personalStorageBytes), remaining = limit - used - reserved;
  const personal = { used_bytes: integer(used), reserved_bytes: integer(reserved), limit_bytes: integer(limit), remaining_bytes: integer(remaining < 0n ? 0n : remaining), over_quota: used + reserved > limit };
  return { owner_user_id: userId, user_id: userId, ...personal, version: 1, personal,
    spaces: rows.map((row) => ({ ...row, used_bytes: integer(BigInt(row.used_bytes)), reserved_bytes: integer(BigInt(row.reserved_bytes)) })) };
}
