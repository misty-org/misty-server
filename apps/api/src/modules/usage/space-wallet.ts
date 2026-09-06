import type { PoolClient } from "pg";
import { entitlementsForTier } from "../entitlements/policy.js";
import { currentPlan } from "./locking.js";
import { nextWeeklyReset, type UsageWallet } from "./wallet.js";

/** Caller holds Space and sorted account locks from lockUsageContext. */
export async function refreshSpaceWallet(tx: PoolClient, options: { spaceId: string; ownerId: string; now: Date; reclaimStale?: boolean }): Promise<UsageWallet> {
  const { spaceId, now } = options;
  const allowance = entitlementsForTier(await currentPlan(tx, options.ownerId, now)).spaceWeeklyAi;
  if (options.reclaimStale !== false) await tx.query(`WITH stale AS (
    UPDATE hosted_ai_reservations SET status='released',settled_at=$2
    WHERE space_id=$1 AND status='reserved' AND COALESCE(lease_expires_at,created_at+INTERVAL '15 minutes')<=$2 RETURNING user_id,reserved_microusd
  ), personal_release AS (
    UPDATE hosted_ai_wallets w SET reserved_microusd=GREATEST(0,w.reserved_microusd-s.total),updated_at=$2
    FROM (SELECT user_id,sum(reserved_microusd) total FROM stale GROUP BY user_id) s WHERE w.user_id=s.user_id
  ) UPDATE space_hosted_ai_wallets SET reserved_microusd=GREATEST(0,reserved_microusd-COALESCE((SELECT sum(reserved_microusd) FROM stale),0)),updated_at=$2 WHERE space_id=$1`, [spaceId, now]);
  await tx.query(`INSERT INTO space_hosted_ai_wallets(space_id,weekly_allowance_microusd,weekly_remaining_microusd,reset_at)
    VALUES($1,$2,$2,$3) ON CONFLICT(space_id) DO NOTHING`, [spaceId, allowance.toString(), nextWeeklyReset(now)]);
  const result = await tx.query<{ weekly_allowance_microusd: string; weekly_remaining_microusd: string; weekly_consumed_microusd: string; reserved_microusd: string; reset_at: Date }>(
    "SELECT weekly_allowance_microusd,weekly_remaining_microusd,weekly_consumed_microusd,reserved_microusd,reset_at FROM space_hosted_ai_wallets WHERE space_id=$1 FOR UPDATE", [spaceId]);
  const prior = result.rows[0]!;
  let consumed = BigInt(prior.weekly_consumed_microusd);
  const observed = BigInt(prior.weekly_allowance_microusd) - BigInt(prior.weekly_remaining_microusd);
  if (observed > consumed) consumed = observed;
  const resetDue = prior.reset_at <= now;
  if (resetDue) consumed = 0n;
  const remaining = allowance > consumed ? allowance - consumed : 0n;
  const resetAt = resetDue ? nextWeeklyReset(now) : prior.reset_at;
  await tx.query(`UPDATE space_hosted_ai_wallets SET weekly_allowance_microusd=$2,weekly_remaining_microusd=$3,
    weekly_consumed_microusd=$4,reset_at=$5,updated_at=$6 WHERE space_id=$1`, [spaceId, allowance.toString(), remaining.toString(), consumed.toString(), resetAt, now]);
  return { allowance, remaining, consumed, reserved: BigInt(prior.reserved_microusd), resetAt };
}
