import type { Pool } from "pg";
import { billingSummarySchema, type BillingSummaryIntent } from "../../../../../packages/service-contracts/src/payments.js";

export class BillingSummaryUnavailable extends Error {}
export class BillingIdentityConflict extends Error {}
export function createBillingSummaryRepository(pool: Pool) {
  return {
    async get(input: BillingSummaryIntent) {
      // One statement provides one snapshot across checkpoints, account identity,
      // effective subscription and purchase status. Runtime cannot write markers.
      const row = (await pool.query<{ history_complete: boolean; license_id: string | null; subscription: unknown; has_completed_purchase: boolean }>(`SELECT
        (SELECT count(*)=2 FROM billing.cutover_checkpoints WHERE name IN ('legacy_purchases_imported','legacy_subscriptions_imported')) AS history_complete,
        (SELECT license_id FROM billing.accounts WHERE user_id=$1) AS license_id,
        (SELECT jsonb_build_object('interval',billing_interval,'status',status,
          'currentPeriodEnd',CASE WHEN current_period_end IS NULL THEN NULL ELSE to_char(current_period_end AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"') END,
          'cancelAtPeriodEnd',cancel_at_period_end,'customerPortalAvailable',(stripe_customer_id<>'' AND NOT EXISTS(SELECT 1 FROM billing.account_closures WHERE user_id=$1)))
         FROM billing.subscriptions WHERE user_id=$1
         ORDER BY CASE WHEN status IN ('active','trialing') THEN 0 WHEN status='past_due' THEN 1 ELSE 2 END,
           updated_at DESC,stripe_subscription_id LIMIT 1) AS subscription,
        EXISTS(SELECT 1 FROM billing.legacy_purchases WHERE user_id=$1 AND status='completed') AS has_completed_purchase`, [input.userId])).rows[0]!;
      if (!row.history_complete) throw new BillingSummaryUnavailable();
      if (row.license_id !== null && row.license_id !== input.licenseId) throw new BillingIdentityConflict();
      return billingSummarySchema.parse({ ...input, subscription: row.subscription, hasCompletedPurchase: row.has_completed_purchase });
    },
  };
}
