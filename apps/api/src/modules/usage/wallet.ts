import { randomUUID } from "node:crypto";
import type { PoolClient } from "pg";
import { entitlementsForTier } from "../entitlements/policy.js";

export function nextWeeklyReset(now: Date): Date {
  const midnight = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()));
  midnight.setUTCDate(midnight.getUTCDate() + ((8 - midnight.getUTCDay()) % 7 || 7));
  return midnight;
}

/** Called inside the same transaction as a license change or usage operation. */
export async function refreshPersonalWallet(tx: PoolClient, options: { userId: string; tier: string; now: Date; ledgerKey: string; reclaimStale?: boolean }) {
  const { userId, now } = options;
  const allowance = entitlementsForTier(options.tier).personalWeeklyAi;
  if (options.reclaimStale !== false) await tx.query(`WITH released AS (
    UPDATE hosted_ai_reservations SET status='released',settled_at=$2
    WHERE user_id=$1 AND space_id IS NULL AND status='reserved' AND COALESCE(lease_expires_at,created_at+INTERVAL '15 minutes')<=$2
    RETURNING reserved_microusd
  ) UPDATE hosted_ai_wallets SET reserved_microusd=GREATEST(0,reserved_microusd-COALESCE((SELECT sum(reserved_microusd) FROM released),0)),updated_at=$2
    WHERE user_id=$1`, [userId, now]);
  const inserted = await tx.query(`INSERT INTO hosted_ai_wallets(user_id,weekly_allowance_microusd,weekly_remaining_microusd,reset_at)
    VALUES($1,$2,$2,$3) ON CONFLICT(user_id) DO NOTHING`, [userId, allowance.toString(), nextWeeklyReset(now)]);
  const selected = await tx.query<{ weekly_allowance_microusd: string; weekly_remaining_microusd: string; weekly_consumed_microusd: string; reserved_microusd: string; reset_at: Date }>(
    "SELECT weekly_allowance_microusd,weekly_remaining_microusd,weekly_consumed_microusd,reserved_microusd,reset_at FROM hosted_ai_wallets WHERE user_id=$1 FOR UPDATE", [userId]);
  const prior = selected.rows[0]!;
  const priorAllowance = BigInt(prior.weekly_allowance_microusd);
  const priorRemaining = BigInt(prior.weekly_remaining_microusd);
  const resetDue = !inserted.rowCount && prior.reset_at <= now;
  let remaining = priorRemaining;
  let resetAt = prior.reset_at;
  // The separate consumption counter survives a downgrade below already-used
  // allowance. The observed balance also accounts for pre-cutover Go writes.
  let consumed = BigInt(prior.weekly_consumed_microusd);
  if (priorAllowance - priorRemaining > consumed) consumed = priorAllowance - priorRemaining;
  if (resetDue) {
    remaining = allowance;
    resetAt = nextWeeklyReset(now);
    consumed = 0n;
  } else {
    remaining = allowance > consumed ? allowance - consumed : 0n;
  }
  await tx.query(`UPDATE hosted_ai_wallets SET weekly_allowance_microusd=$2,weekly_remaining_microusd=$3,reset_at=$4,updated_at=$5,weekly_consumed_microusd=$6 WHERE user_id=$1`,
    [userId, allowance.toString(), remaining.toString(), resetAt, now, consumed.toString()]);
  if (inserted.rowCount || resetDue || remaining !== priorRemaining) {
    const grant = Boolean(inserted.rowCount || resetDue);
    const key = grant ? `weekly_grant:${userId}:${resetAt.toISOString().replace(".000Z", "Z")}` : `plan_adjustment:${userId}:${options.ledgerKey}`;
    await tx.query(`INSERT INTO hosted_ai_usage_ledger(id,user_id,source,weekly_delta_microusd,idempotency_key)
      VALUES($1,$2,$3,$4,$5) ON CONFLICT(idempotency_key) DO NOTHING`,
      [randomUUID(), userId, grant ? "weekly_grant" : "plan_adjustment", (grant ? allowance : remaining - priorRemaining).toString(), key]);
  }
  return { allowance, remaining, consumed, reserved: BigInt(prior.reserved_microusd), resetAt };
}

export type UsageWallet = Awaited<ReturnType<typeof refreshPersonalWallet>>;
export function available(wallet: UsageWallet): bigint { return wallet.remaining > wallet.reserved ? wallet.remaining - wallet.reserved : 0n; }
