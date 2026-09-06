import type { PoolClient } from "pg";
import { subscriptionProjectionSchema } from "../../../../../packages/service-contracts/src/payments.js";

export async function effectiveSubscription(tx: PoolClient, userId: string) {
  // Preserve Go's effective-subscription preference: paid, then past_due, then others.
  const effective = await tx.query<{ subscription: unknown }>(`SELECT jsonb_build_object(
    'subscriptionId',stripe_subscription_id,'tier',tier,'interval',billing_interval,'status',status,
    'currentPeriodEnd',CASE WHEN current_period_end IS NULL THEN NULL ELSE to_char(current_period_end AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"') END,
    'cancelAtPeriodEnd',cancel_at_period_end) AS subscription FROM billing.subscriptions WHERE user_id=$1
    ORDER BY CASE WHEN status IN ('active','trialing') THEN 0 WHEN status='past_due' THEN 1 ELSE 2 END,
      updated_at DESC,stripe_subscription_id LIMIT 1`, [userId]);
  return effective.rows[0] ? subscriptionProjectionSchema.parse(effective.rows[0].subscription) : null;
}
