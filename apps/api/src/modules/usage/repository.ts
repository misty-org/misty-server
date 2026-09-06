import { randomUUID } from "node:crypto";
import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { currentPlan, lockUsageContext } from "./locking.js";
import { available, refreshPersonalWallet, type UsageWallet } from "./wallet.js";
import { refreshSpaceWallet } from "./space-wallet.js";
import { defaultRateCardVersion, UsageConflict, UsageLimitReached, UsageNotFound, usageFingerprint, validateAmount, validateKey, type Reservation, type Usage } from "./model.js";

type Handle = Pick<Reservation, "id" | "userId" | "generation">;
type ReservationRow = { id: string; user_id: string; space_id: string | null; reserved_microusd: string; generation: number; status: string;
  meter: string; requested_microusd: string | null; allow_partial: boolean | null };
const reservationColumns = "id,user_id,space_id,reserved_microusd,generation,status,meter,requested_microusd,allow_partial";
const reservation = (row: ReservationRow): Reservation => ({ id: row.id, userId: row.user_id, spaceId: row.space_id,
  amount: BigInt(row.reserved_microusd), generation: row.generation, status: row.status });
const maxZero = (value: bigint) => value > 0n ? value : 0n;
const min = (a: bigint, b: bigint) => a < b ? a : b;

async function writeReserved(tx: PoolClient, userId: string, spaceId: string | null, delta: bigint, now: Date) {
  await tx.query("UPDATE hosted_ai_wallets SET reserved_microusd=GREATEST(0,reserved_microusd+$2),updated_at=$3 WHERE user_id=$1", [userId, delta.toString(), now]);
  if (spaceId) await tx.query("UPDATE space_hosted_ai_wallets SET reserved_microusd=GREATEST(0,reserved_microusd+$2),updated_at=$3 WHERE space_id=$1", [spaceId, delta.toString(), now]);
}
async function refreshWallets(tx: PoolClient, userId: string, context: { spaceId: string | null; ownerId: string | undefined }, now: Date, key: string, reclaimStale: boolean) {
  const space = context.spaceId && context.ownerId ? await refreshSpaceWallet(tx, { spaceId: context.spaceId, ownerId: context.ownerId, now, reclaimStale }) : null;
  const personal = await refreshPersonalWallet(tx, { userId, tier: await currentPlan(tx, userId, now), now, ledgerKey: key, reclaimStale });
  return { personal, space };
}
async function lockReservation(tx: PoolClient, handle: Handle, key: string, now: Date) {
  const initial = await tx.query<ReservationRow>(`SELECT ${reservationColumns} FROM hosted_ai_reservations WHERE id=$1 AND user_id=$2`, [handle.id, handle.userId]);
  if (!initial.rows[0]) throw new UsageNotFound("Reservation is unavailable");
  const context = await lockUsageContext(tx, { userId: handle.userId, spaceId: initial.rows[0].space_id, key, now, requireMembership: false });
  const wallets = await refreshWallets(tx, handle.userId, context, now, key, false);
  const current = await tx.query<ReservationRow>(`SELECT ${reservationColumns} FROM hosted_ai_reservations WHERE id=$1 AND user_id=$2 FOR UPDATE`, [handle.id, handle.userId]);
  const row = current.rows[0];
  if (!row) throw new UsageNotFound("Reservation is unavailable");
  if (row.generation !== handle.generation || row.space_id !== context.spaceId) throw new UsageConflict("Reservation generation or Space changed");
  return { row, ...wallets };
}
async function ledgerKey(tx: PoolClient, key: string, reservationId: string, source: string, fingerprint?: string) {
  const result = await tx.query<{ reservation_id: string | null; source: string; request_sha256: string | null }>(
    "SELECT reservation_id,source,request_sha256 FROM hosted_ai_usage_ledger WHERE idempotency_key=$1", [key]);
  const prior = result.rows[0];
  if (prior && (prior.reservation_id !== reservationId || prior.source !== source || (fingerprint && prior.request_sha256 && prior.request_sha256 !== fingerprint))) {
    throw new UsageConflict("Usage idempotency key belongs to a different operation");
  }
  return Boolean(prior);
}
async function applyCharge(tx: PoolClient, userId: string, spaceId: string | null, charge: bigint, reserved: bigint, now: Date) {
  await tx.query(`UPDATE hosted_ai_wallets SET weekly_remaining_microusd=weekly_remaining_microusd-$2,
    weekly_consumed_microusd=weekly_consumed_microusd+$2,reserved_microusd=GREATEST(0,reserved_microusd-$3),updated_at=$4 WHERE user_id=$1`,
    [userId, charge.toString(), reserved.toString(), now]);
  if (spaceId) await tx.query(`UPDATE space_hosted_ai_wallets SET weekly_remaining_microusd=weekly_remaining_microusd-$2,
    weekly_consumed_microusd=weekly_consumed_microusd+$2,reserved_microusd=GREATEST(0,reserved_microusd-$3),updated_at=$4 WHERE space_id=$1`,
    [spaceId, charge.toString(), reserved.toString(), now]);
}

export function createUsageRepository(options: { pool: Pool; now?: () => Date; rateCardVersion?: string }) {
  const clock = options.now ?? (() => new Date());
  const rateCard = options.rateCardVersion ?? defaultRateCardVersion;
  return {
    async wallets(input: { userId: string; spaceId?: string }) {
      const now = clock(), spaceId = input.spaceId?.trim() || null;
      const key = `wallet:${input.userId}:${spaceId ?? "personal"}`;
      return withTransaction(options.pool, async (tx) => {
        const context = await lockUsageContext(tx, { userId: input.userId, spaceId, key, now, requireMembership: true });
        return refreshWallets(tx, input.userId, context, now, key, true);
      }, { mode: "service" });
    },

    async reserve(input: { userId: string; spaceId?: string; meter: string; idempotencyKey: string; amount: bigint; allowPartial?: boolean }) {
      validateAmount(input.amount, true); validateKey(input.idempotencyKey);
      if (!input.meter.trim() || input.meter.length > 128) throw new Error("Invalid usage meter");
      const now = clock(), spaceId = input.spaceId?.trim() || null, partial = input.allowPartial ?? false;
      return withTransaction(options.pool, async (tx) => {
        const context = await lockUsageContext(tx, { userId: input.userId, spaceId, key: input.idempotencyKey, now, requireMembership: true });
        const { personal, space } = await refreshWallets(tx, input.userId, context, now, input.idempotencyKey, true);
        const existing = await tx.query<ReservationRow>(`SELECT ${reservationColumns} FROM hosted_ai_reservations WHERE idempotency_key=$1 FOR UPDATE`, [input.idempotencyKey]);
        const prior = existing.rows[0];
        if (prior) {
          const amountMatches = prior.requested_microusd !== null
            ? BigInt(prior.requested_microusd) === input.amount && prior.allow_partial === partial
            : BigInt(prior.reserved_microusd) === input.amount || (partial && BigInt(prior.reserved_microusd) < input.amount);
          if (prior.user_id !== input.userId || prior.space_id !== spaceId || prior.meter !== input.meter || !amountMatches) throw new UsageConflict("Reservation parameters changed");
          if (prior.status !== "released") return { reservation: reservation(prior), wallet: personal };
        }
        const personalAvailable = available(personal), spaceAvailable = space ? available(space) : personalAvailable;
        const budget = min(personalAvailable, spaceAvailable);
        if (budget < input.amount && (!partial || budget === 0n)) throw new UsageLimitReached(input.amount, budget, spaceAvailable < personalAvailable ? "space" : "personal");
        const amount = min(input.amount, budget);
        let saved: ReservationRow;
        if (prior) {
          saved = (await tx.query<ReservationRow>(`UPDATE hosted_ai_reservations SET reserved_microusd=$2,status='reserved',generation=generation+1,
            requested_microusd=$3,allow_partial=$4,created_at=$5,lease_expires_at=$5::timestamptz+INTERVAL '15 minutes',settled_at=NULL WHERE id=$1 RETURNING ${reservationColumns}`,
            [prior.id, amount.toString(), input.amount.toString(), partial, now])).rows[0]!;
        } else {
          saved = (await tx.query<ReservationRow>(`INSERT INTO hosted_ai_reservations(id,user_id,space_id,idempotency_key,meter,reserved_microusd,requested_microusd,allow_partial,created_at,lease_expires_at)
            VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9::timestamptz+INTERVAL '15 minutes') RETURNING ${reservationColumns}`,
            [randomUUID(), input.userId, spaceId, input.idempotencyKey, input.meter, amount.toString(), input.amount.toString(), partial, now])).rows[0]!;
        }
        await writeReserved(tx, input.userId, spaceId, amount, now);
        return { reservation: reservation(saved), wallet: { ...personal, reserved: personal.reserved + amount } };
      }, { mode: "service" });
    },

    async release(handle: Handle) {
      const now = clock();
      return withTransaction(options.pool, async (tx) => {
        const { row, personal } = await lockReservation(tx, handle, `reservation:${handle.id}`, now);
        if (row.status !== "reserved") return personal;
        const amount = BigInt(row.reserved_microusd);
        await writeReserved(tx, row.user_id, row.space_id, -amount, now);
        await tx.query("UPDATE hosted_ai_reservations SET status='released',settled_at=$2 WHERE id=$1", [row.id, now]);
        return { ...personal, reserved: maxZero(personal.reserved - amount) };
      }, { mode: "service" });
    },

    async renew(handle: Handle): Promise<boolean> {
      const now = clock();
      return withTransaction(options.pool, async (tx) => {
        const { row } = await lockReservation(tx, handle, `reservation:${handle.id}`, now);
        const updated = await tx.query(`UPDATE hosted_ai_reservations SET lease_expires_at=$2::timestamptz+INTERVAL '15 minutes' WHERE id=$1 AND status='reserved'
          AND COALESCE(lease_expires_at,created_at+INTERVAL '15 minutes')>$2`, [row.id, now]);
        return Boolean(updated.rowCount);
      }, { mode: "service" });
    },

    async settle(handle: Handle, key: string, usage: Usage) {
      validateKey(key);
      const fingerprint = usageFingerprint(usage), now = clock();
      return withTransaction(options.pool, async (tx) => {
        const { row, personal, space } = await lockReservation(tx, handle, key, now);
        const duplicate = await ledgerKey(tx, key, row.id, "consumption", fingerprint);
        if (duplicate || row.status !== "reserved") return personal;
        const reserved = BigInt(row.reserved_microusd);
        let maximum = maxZero(personal.remaining - maxZero(personal.reserved - reserved));
        if (space) maximum = min(maximum, maxZero(space.remaining - maxZero(space.reserved - reserved)));
        const charge = min(usage.chargeMicrousd > 0n ? usage.chargeMicrousd : 1n, maximum);
        await applyCharge(tx, row.user_id, row.space_id, charge, reserved, now);
        await tx.query("UPDATE hosted_ai_reservations SET status='settled',settled_at=$2 WHERE id=$1", [row.id, now]);
        await tx.query(`INSERT INTO hosted_ai_usage_ledger(id,user_id,space_id,reservation_id,source,meter,weekly_delta_microusd,provider,model,
          input_tokens,cached_input_tokens,output_tokens,reasoning_tokens,rate_card_version,provider_cost_microusd,charged_microusd,idempotency_key,
          personal_reset_at,space_reset_at,space_delta_microusd,request_sha256,created_at)
          VALUES($1,$2,$3,$4,'consumption',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`,
          [randomUUID(), row.user_id, row.space_id, row.id, row.meter, (-charge).toString(), usage.provider, usage.model,
            usage.inputTokens.toString(), usage.cachedInputTokens.toString(), usage.outputTokens.toString(), usage.reasoningTokens.toString(),
            rateCard, usage.providerCost.toString(), charge.toString(), key, personal.resetAt, space?.resetAt ?? null, space ? (-charge).toString() : "0", fingerprint, now]);
        return { ...personal, remaining: personal.remaining - charge, consumed: personal.consumed + charge, reserved: maxZero(personal.reserved - reserved) };
      }, { mode: "service" });
    },

    async refund(handle: Handle, key: string, reason: string) {
      validateKey(key);
      if (!/^[a-z0-9_:-]{1,100}$/.test(reason)) throw new Error("Invalid internal usage refund reason");
      const source = `internal_failure_refund:${reason}`, now = clock();
      return withTransaction(options.pool, async (tx) => {
        const { row, personal, space } = await lockReservation(tx, handle, key, now);
        const duplicate = await ledgerKey(tx, key, row.id, source);
        if (duplicate || row.status === "refunded") return personal;
        if (row.status !== "settled") throw new UsageConflict("Reservation is not settled");
        const charged = await tx.query<{ charged_microusd: string; personal_reset_at: Date | null; space_reset_at: Date | null; meter: string; provider: string; model: string }>(
          "SELECT charged_microusd,personal_reset_at,space_reset_at,meter,provider,model FROM hosted_ai_usage_ledger WHERE reservation_id=$1 AND source='consumption' ORDER BY created_at LIMIT 1", [row.id]);
        const entry = charged.rows[0];
        if (!entry) throw new UsageConflict("Consumption record is unavailable");
        if (!entry.personal_reset_at || (space && !entry.space_reset_at)) throw new UsageConflict("Historical usage period requires verification");
        const amount = BigInt(entry.charged_microusd);
        const refundWallet = (wallet: UsageWallet, period: Date | null) => {
          const consumed = period?.getTime() === wallet.resetAt.getTime() ? maxZero(wallet.consumed - amount) : wallet.consumed;
          const remaining = maxZero(wallet.allowance - consumed);
          return { ...wallet, consumed, remaining, delta: remaining - wallet.remaining };
        };
        const updated = refundWallet(personal, entry.personal_reset_at);
        const updatedSpace = space ? refundWallet(space, entry.space_reset_at) : null;
        await tx.query("UPDATE hosted_ai_wallets SET weekly_remaining_microusd=$2,weekly_consumed_microusd=$3,updated_at=$4 WHERE user_id=$1", [row.user_id, updated.remaining.toString(), updated.consumed.toString(), now]);
        if (updatedSpace) await tx.query("UPDATE space_hosted_ai_wallets SET weekly_remaining_microusd=$2,weekly_consumed_microusd=$3,updated_at=$4 WHERE space_id=$1", [row.space_id, updatedSpace.remaining.toString(), updatedSpace.consumed.toString(), now]);
        await tx.query(`INSERT INTO hosted_ai_usage_ledger(id,user_id,space_id,reservation_id,source,meter,weekly_delta_microusd,provider,model,
          rate_card_version,idempotency_key,personal_reset_at,space_reset_at,space_delta_microusd,created_at)
          VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
          [randomUUID(), row.user_id, row.space_id, row.id, source, entry.meter, updated.delta.toString(), entry.provider, entry.model,
            rateCard, key, personal.resetAt, space?.resetAt ?? null, updatedSpace?.delta.toString() ?? "0", now]);
        await tx.query("UPDATE hosted_ai_reservations SET status='refunded' WHERE id=$1", [row.id]);
        return updated;
      }, { mode: "service" });
    },
  };
}
