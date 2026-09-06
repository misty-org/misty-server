import type { PoolClient } from "pg";
import { UsageForbidden, UsageNotFound } from "./model.js";
import { normalizePlan } from "../entitlements/policy.js";

/** Lock order for all native usage operations: idempotency key, Space, sorted
 * accounts, wallets, reservation. Entitlement effects lock account then wallet.
 * Space ownership writers must take the Space lock before these account locks.
 */
export async function lockUsageContext(tx: PoolClient, options: { userId: string; spaceId: string | null; key: string; now: Date; requireMembership: boolean }) {
  await tx.query("SELECT pg_advisory_xact_lock(hashtextextended($1,0))", [`usage:${options.key}`]);
  let ownerId: string | undefined;
  let spaceId = options.spaceId;
  let staleUsers: string[] = [];
  if (spaceId) {
    const space = await tx.query<{ owner_user_id: string; lifecycle_state: string }>("SELECT owner_user_id,lifecycle_state FROM spaces WHERE id=$1 FOR UPDATE", [spaceId]);
    if (!space.rows[0]) {
      if (options.requireMembership) throw new UsageNotFound("Space is unavailable");
      spaceId = null; // Historical reservation after Space deletion.
    } else {
      if (options.requireMembership && space.rows[0].lifecycle_state !== "active") throw new UsageForbidden("Space is unavailable");
      ownerId = space.rows[0].owner_user_id;
      if (options.requireMembership) staleUsers = (await tx.query<{ user_id: string }>(`SELECT DISTINCT user_id FROM hosted_ai_reservations
        WHERE space_id=$1 AND status='reserved' AND COALESCE(lease_expires_at,created_at+INTERVAL '15 minutes')<=$2`, [spaceId, options.now])).rows.map((row) => row.user_id);
    }
  }
  const ids = [...new Set([options.userId, ...(ownerId ? [ownerId] : []), ...staleUsers])].sort();
  const accounts = await tx.query<{ id: string; lifecycle_state: string }>("SELECT id,lifecycle_state FROM users WHERE id=ANY($1::text[]) ORDER BY id FOR UPDATE", [ids]);
  const account = accounts.rows.find((row) => row.id === options.userId);
  if (!account) throw new UsageNotFound("Account is unavailable");
  if (options.requireMembership && account.lifecycle_state !== "active") throw new UsageForbidden("Account is unavailable");
  if (spaceId && options.requireMembership) {
    const member = await tx.query("SELECT 1 FROM space_members WHERE space_id=$1 AND user_id=$2", [spaceId, options.userId]);
    if (!member.rowCount) throw new UsageForbidden("Space membership is required");
  }
  return { spaceId, ownerId };
}

export async function currentPlan(tx: PoolClient, userId: string, now: Date) {
  const result = await tx.query<{ tier: string; status: string; expires_at: Date | null; legacy_tier: string | null }>(
    "SELECT tier,status,expires_at,legacy_tier FROM licenses WHERE user_id=$1", [userId]);
  const license = result.rows[0];
  if (!license) throw new UsageNotFound("License is unavailable");
  return normalizePlan(license.status === "trialing" && license.expires_at && license.expires_at <= now ? license.legacy_tier : license.tier);
}
