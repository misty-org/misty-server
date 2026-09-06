import type { PoolClient } from "pg";
import { subscriptionEntitlementEventSchema, type PaymentEntitlementEvent, type PurchaseReversalEvent } from "../../../../../packages/service-contracts/src/payments.js";
import { refreshPersonalWallet } from "../usage/wallet.js";
import { normalizePlan, subscriptionLicenseState } from "./policy.js";

export class LifetimeGrantAttributionPending extends Error {}
export class LifetimeGrantConflict extends Error {}

async function license(tx: PoolClient, userId: string, licenseId: string) {
  const result = await tx.query<{ tier: string; status: string; expires_at: Date | null; legacy_tier: string | null }>(
    "SELECT tier,status,expires_at,legacy_tier FROM licenses WHERE id=$1 AND user_id=$2 FOR UPDATE", [licenseId, userId]);
  if (!result.rows[0]) throw new Error("License identity is unavailable");
  return result.rows[0];
}
async function storeLicense(tx: PoolClient, event: { userId: string; licenseId: string; eventId: string }, state: ReturnType<typeof subscriptionLicenseState>, now: Date, recordTrial: boolean) {
  await tx.query(`UPDATE licenses SET tier=$2,status=$3,expires_at=$4,
    trial_started_at=CASE WHEN $5 THEN COALESCE(trial_started_at,$6) ELSE trial_started_at END,updated_at=$6 WHERE id=$1`,
    [event.licenseId, state.tier, state.status, state.expiresAt, recordTrial, now]);
  await refreshPersonalWallet(tx, { userId: event.userId, tier: state.tier, now, ledgerKey: event.eventId });
}
async function lifetimeTier(tx: PoolClient, licenseId: string) {
  const result = await tx.query<{ tier: string }>(`SELECT tier FROM license_lifetime_grants WHERE license_id=$1 AND revoked_at IS NULL
    ORDER BY CASE tier WHEN 'max' THEN 3 WHEN 'pro' THEN 2 WHEN 'personal' THEN 2 ELSE 1 END DESC,tier DESC LIMIT 1`, [licenseId]);
  return result.rows[0]?.tier ?? null;
}

export function createEntitlementEffects(options: { now?: () => Date } = {}) {
  const now = options.now ?? (() => new Date());
  return {
    async applyEntitlements(tx: PoolClient, event: PaymentEntitlementEvent, ledgerKey = event.eventId) {
      const current = await license(tx, event.userId, event.licenseId);
      const time = now();
      await storeLicense(tx, { ...event, eventId: ledgerKey }, subscriptionLicenseState(event.subscription, current.legacy_tier, time), time, event.subscription?.status === "trialing");
    },
    async applyPurchaseReversal(tx: PoolClient, event: PurchaseReversalEvent, accountActive = true) {
      const current = await license(tx, event.userId, event.licenseId);
      const grant = await tx.query<{ id: string; user_id: string; license_id: string }>(
        "SELECT id,user_id,license_id FROM license_lifetime_grants WHERE source='purchase' AND source_id=$1 FOR UPDATE", [event.purchaseId]);
      if (!grant.rows[0]) throw new LifetimeGrantAttributionPending("Purchase grant attribution requires verification");
      if (grant.rows[0].user_id !== event.userId || grant.rows[0].license_id !== event.licenseId) throw new LifetimeGrantConflict("Purchase grant identity conflicts");
      if (normalizePlan(await lifetimeTier(tx, event.licenseId)) !== normalizePlan(current.legacy_tier)) {
        throw new LifetimeGrantConflict("Lifetime grant projection changed outside its owner");
      }
      const time = now();
      await tx.query("UPDATE license_lifetime_grants SET revoked_at=$2,reversal_event_id=$3 WHERE id=$1 AND revoked_at IS NULL", [grant.rows[0].id, time, event.eventId]);
      const fallback = await lifetimeTier(tx, event.licenseId);
      await tx.query("UPDATE licenses SET legacy_tier=$2,updated_at=$3 WHERE id=$1", [event.licenseId, fallback, time]);
      // Preserve financial grant/reversal attribution after account deletion,
      // but do not refresh access or allowance for an inactive account.
      if (!accountActive) return;
      const projection = await tx.query<{ payload: unknown }>("SELECT payload FROM payment_entitlement_projections WHERE user_id=$1", [event.userId]);
      const subscription = projection.rows[0] ? subscriptionEntitlementEventSchema.parse(projection.rows[0].payload).subscription : null;
      // A local trial without a payment projection belongs to the API and survives
      // reversal of an unrelated historical purchase until its own expiry.
      const state = !projection.rows[0] && current.status === "trialing" && current.expires_at && current.expires_at > time
        ? { tier: normalizePlan(current.tier), status: "trialing" as const, expiresAt: current.expires_at }
        : subscriptionLicenseState(subscription, fallback, time);
      await storeLicense(tx, event, state, time, false);
    },
  };
}
