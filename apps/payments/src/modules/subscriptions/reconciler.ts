import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { synchronizeSubscription, type BillingAccount } from "./repository.js";
import { SubscriptionValidationError, type PriceCatalog } from "./model.js";

export function createSubscriptionReconciler(options: {
  pool: Pool;
  catalog: PriceCatalog;
  fetchSubscription: (id: string) => Promise<unknown>;
}) {
  return {
    async runOnce(): Promise<boolean> {
      return withTransaction(options.pool, async (tx) => {
        // Lock the account first, matching webhook/checkout lock order. Another
        // worker skips it instead of making competing canonical Stripe reads.
        const due = await tx.query<BillingAccount & { stripe_subscription_id: string; reconcile_failures: number }>(`
          SELECT a.user_id,a.license_id,a.stripe_customer_id,s.stripe_subscription_id,s.reconcile_failures
          FROM billing.subscriptions s JOIN billing.accounts a ON a.user_id=s.user_id
          WHERE s.status IN ('trialing','active','past_due') AND s.reconcile_after<=now()
          ORDER BY s.reconcile_after,s.stripe_subscription_id FOR UPDATE OF a SKIP LOCKED LIMIT 1`);
        const account = due.rows[0];
        if (!account) return false;
        await tx.query("SAVEPOINT reconcile_subscription");
        try {
          await synchronizeSubscription(tx, {
            account, subscriptionId: account.stripe_subscription_id,
            catalog: options.catalog, fetchSubscription: options.fetchSubscription,
          });
          await tx.query("UPDATE billing.subscriptions SET reconcile_failures=0,last_reconcile_error=NULL WHERE stripe_subscription_id=$1", [account.stripe_subscription_id]);
          await tx.query("RELEASE SAVEPOINT reconcile_subscription");
        } catch (error) {
          await tx.query("ROLLBACK TO SAVEPOINT reconcile_subscription");
          const delayMinutes = 15 * 2 ** Math.min(account.reconcile_failures, 5);
          await tx.query(`UPDATE billing.subscriptions SET reconcile_failures=reconcile_failures+1,
            last_reconcile_error=$2,reconcile_after=now()+($3::integer*INTERVAL '1 minute') WHERE stripe_subscription_id=$1`,
            [account.stripe_subscription_id, error instanceof SubscriptionValidationError ? error.code : "stripe_unavailable", delayMinutes]);
        }
        return true;
      });
    },
  };
}
