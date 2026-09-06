import type { PoolClient } from "pg";
import { effectiveSubscription } from "./projection.js";
import { enqueueEntitlement } from "../entitlements/repository.js";
import { nextReconciliation, normalizeSubscription, SubscriptionValidationError, type PriceCatalog } from "./model.js";

export interface BillingAccount { user_id: string; license_id: string; stripe_customer_id: string | null }
export async function lockBillingAccount(tx: PoolClient, userId: string): Promise<BillingAccount> {
  const result = await tx.query<BillingAccount>("SELECT user_id,license_id,stripe_customer_id FROM billing.accounts WHERE user_id=$1 FOR UPDATE", [userId]);
  if (!result.rows[0]) throw new SubscriptionValidationError("subscription_identity_mismatch");
  return result.rows[0];
}

/** Fetch canonical Stripe state AFTER obtaining the account lock. Older webhook
 * deliveries become wakeups, so arrival order cannot restore a canceled plan. */
export async function synchronizeSubscription(tx: PoolClient, options: {
  account: BillingAccount;
  subscriptionId: string;
  catalog: PriceCatalog;
  fetchSubscription: (id: string) => Promise<unknown>;
}) {
  const canonical = normalizeSubscription(await options.fetchSubscription(options.subscriptionId), options.catalog);
  const account = options.account;
  const projection = canonical.projection;
  if (canonical.userId !== account.user_id || canonical.licenseId !== account.license_id ||
      projection.subscriptionId !== options.subscriptionId ||
      (account.stripe_customer_id !== null && account.stripe_customer_id !== canonical.customerId)) {
    throw new SubscriptionValidationError("subscription_identity_mismatch");
  }
  const previous = await tx.query<{ user_id: string; unchanged: boolean }>(`SELECT user_id,
    (stripe_customer_id=$2 AND stripe_price_id=$3 AND tier=$4 AND billing_interval=$5 AND status=$6
     AND current_period_end IS NOT DISTINCT FROM $7::timestamptz AND cancel_at_period_end=$8
     AND canceled_at IS NOT DISTINCT FROM $9::timestamptz) AS unchanged
    FROM billing.subscriptions WHERE stripe_subscription_id=$1 FOR UPDATE`,
    [projection.subscriptionId, canonical.customerId, canonical.priceId, projection.tier, projection.interval,
      projection.status, projection.currentPeriodEnd, projection.cancelAtPeriodEnd, canonical.canceledAt]);
  if (previous.rows[0] && previous.rows[0].user_id !== account.user_id) throw new SubscriptionValidationError("subscription_identity_mismatch");
  const now = new Date();
  if (previous.rows[0]?.unchanged) {
    await tx.query("UPDATE billing.subscriptions SET last_reconciled_at=now(),reconcile_after=$2 WHERE stripe_subscription_id=$1",
      [projection.subscriptionId, nextReconciliation(now, projection.currentPeriodEnd)]);
    // Imported unchanged rows still need their first snapshot. The account lock
    // serializes this check with every producer; reversal-only revisions do not
    // imply that a subscription snapshot has ever been queued.
    const initialized = await tx.query<{ subscription_snapshot_enqueued: boolean }>(
      "SELECT subscription_snapshot_enqueued FROM billing.accounts WHERE user_id=$1", [account.user_id]);
    if (!initialized.rows[0]?.subscription_snapshot_enqueued) {
      await enqueueEntitlement(tx, { userId: account.user_id, licenseId: account.license_id,
        subscription: await effectiveSubscription(tx, account.user_id) });
    }
    return false;
  }
  if (account.stripe_customer_id === null) {
    await tx.query("UPDATE billing.accounts SET stripe_customer_id=$2,updated_at=now() WHERE user_id=$1", [account.user_id, canonical.customerId]);
  }
  await tx.query(`INSERT INTO billing.subscriptions(stripe_subscription_id,user_id,stripe_customer_id,stripe_price_id,
    tier,billing_interval,status,current_period_end,cancel_at_period_end,canceled_at,reconcile_after,last_reconciled_at)
    VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now()) ON CONFLICT(stripe_subscription_id) DO UPDATE SET
    stripe_customer_id=EXCLUDED.stripe_customer_id,stripe_price_id=EXCLUDED.stripe_price_id,tier=EXCLUDED.tier,
    billing_interval=EXCLUDED.billing_interval,status=EXCLUDED.status,current_period_end=EXCLUDED.current_period_end,
    cancel_at_period_end=EXCLUDED.cancel_at_period_end,canceled_at=EXCLUDED.canceled_at,
    reconcile_after=EXCLUDED.reconcile_after,last_reconciled_at=now(),updated_at=now()`,
    [projection.subscriptionId, account.user_id, canonical.customerId, canonical.priceId, projection.tier,
      projection.interval, projection.status, projection.currentPeriodEnd, projection.cancelAtPeriodEnd,
      canonical.canceledAt, nextReconciliation(now, projection.currentPeriodEnd)]);

  await enqueueEntitlement(tx, {
    userId: account.user_id, licenseId: account.license_id,
    subscription: await effectiveSubscription(tx, account.user_id),
  });
  return true;
}
