import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { enqueueEntitlement } from "../entitlements/repository.js";
import { SubscriptionValidationError, type PriceCatalog } from "../subscriptions/model.js";
import { ClosureCleanupError, type ClosureAccount, type ClosureGateway, type ClosureResource } from "./model.js";
import { assertClosureResourceOwner, seedClosureResources, unresolvedClosureCheckouts } from "./resources.js";
import { closeCheckout } from "./checkouts.js";
import { closeSubscription } from "./subscriptions.js";
import { closeCustomer } from "./customers.js";

/** One resource or bounded page per account-locked transaction. Remote effects
 * are retried through canonical reads; never infer success from an HTTP timeout. */
export function createBillingClosureWorker(options: { pool: Pool; catalog: PriceCatalog; gateway: ClosureGateway }) {
  return {
    async runOnce(): Promise<boolean> {
      return withTransaction(options.pool, async tx => {
        await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
        const account = (await tx.query<ClosureAccount>(`SELECT a.user_id,a.license_id,a.stripe_customer_id,c.license_id AS closure_license_id,c.attempts
          FROM billing.accounts a JOIN billing.account_closures c ON c.user_id=a.user_id WHERE c.state='closing' AND c.available_at<=clock_timestamp()
          ORDER BY c.available_at,a.user_id FOR UPDATE OF a SKIP LOCKED LIMIT 1`)).rows[0];
        if (!account) return false;
        const current = await tx.query("SELECT user_id FROM billing.account_closures WHERE user_id=$1 AND state='closing' AND available_at<=clock_timestamp() FOR UPDATE", [account.user_id]);
        if (!current.rowCount) return false;
        const signal = AbortSignal.timeout(45000);
        await tx.query("SAVEPOINT closure_effect");
        try {
          if (account.license_id !== account.closure_license_id) throw new ClosureCleanupError("closure_identity_mismatch");
          const history = await tx.query("SELECT 1 FROM billing.cutover_checkpoints WHERE name IN ('legacy_purchases_imported','legacy_subscriptions_imported','legacy_checkouts_imported')");
          if (history.rowCount !== 3) throw new ClosureCleanupError("closure_history_unavailable");
          await seedClosureResources(tx, account.user_id);
          const resource = (await tx.query<ClosureResource>(`SELECT kind,resource_id,customer_id,phase,cursor,pages FROM billing.account_closure_resources
            WHERE user_id=$1 AND state='pending' ORDER BY CASE kind WHEN 'checkout' THEN 0 WHEN 'subscription' THEN 1 ELSE 2 END,resource_id LIMIT 1 FOR UPDATE`, [account.user_id])).rows[0];
          signal.throwIfAborted();
          if (resource) {
            await assertClosureResourceOwner(tx, account.user_id, resource.kind, resource.resource_id);
            if (resource.kind === "checkout") await closeCheckout(tx, account, resource, options.gateway);
            else if (resource.kind === "subscription") await closeSubscription(tx, account, resource, options.gateway, options.catalog);
            else await closeCustomer(tx, account, resource, options.gateway, signal);
            signal.throwIfAborted();
            await tx.query("UPDATE billing.account_closures SET attempts=0,last_error_code='',available_at=now(),updated_at=now() WHERE user_id=$1", [account.user_id]);
          } else {
            if (await unresolvedClosureCheckouts(tx, account.user_id)) throw new ClosureCleanupError("closure_recovery_required");
            // No unresolved create remains, and admission is permanently closed.
            // Exact request parameters (including email) are no longer needed for
            // idempotent replay; keep identifiers and financial history only.
            await tx.query("UPDATE billing.checkout_attempts SET stripe_parameters='{}',checkout_url='',updated_at=now() WHERE user_id=$1", [account.user_id]);
            await enqueueEntitlement(tx, { userId: account.user_id, licenseId: account.license_id, subscription: null });
            await tx.query("UPDATE billing.account_closures SET state='closed',completed_at=now(),attempts=0,last_error_code='',updated_at=now() WHERE user_id=$1", [account.user_id]);
          }
          await tx.query("RELEASE SAVEPOINT closure_effect");
        } catch (error) {
          await tx.query("ROLLBACK TO SAVEPOINT closure_effect");
          const code = error instanceof ClosureCleanupError ? error.code : error instanceof SubscriptionValidationError ? "closure_identity_mismatch" : "closure_provider_unavailable";
          await tx.query(`UPDATE billing.account_closures SET attempts=attempts+1,last_error_code=$2,
            available_at=clock_timestamp()+make_interval(secs=>$3::integer),updated_at=now() WHERE user_id=$1`, [account.user_id, code, Math.min(3600, 15 * 2 ** Math.min(account.attempts, 8))]);
        }
        return true;
      });
    },
  };
}
