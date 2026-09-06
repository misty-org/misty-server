import type { Pool } from "pg";
import { z } from "zod";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { synchronizeSubscription } from "../subscriptions/repository.js";
import type { PriceCatalog } from "../subscriptions/model.js";
import type { CheckoutAttempt } from "./repository.js";

export const recoverySessionSchema = z.object({
  id: z.string().min(1), status: z.enum(["open", "complete", "expired"]).nullable(),
  mode: z.enum(["payment", "setup", "subscription"]),
  url: z.url().refine((value) => { const url = new URL(value); return url.protocol === "https:" && !url.username && !url.password; }).nullable(),
  expires_at: z.number().int().positive(),
  subscription: z.union([z.string(), z.object({ id: z.string() })]).nullable(),
  metadata: z.record(z.string(), z.string()).nullable(), client_reference_id: z.string().nullable(),
});
const pageSchema = z.object({ data: z.array(recoverySessionSchema).max(100), has_more: z.boolean() });
export type RecoverySession = z.infer<typeof recoverySessionSchema>;

/** Resolve lost Checkout creation responses without ever reusing an expired
 * idempotency key. One bounded page per transaction; the cursor survives restarts. */
export function createCheckoutRecovery(options: {
  pool: Pool;
  catalog: PriceCatalog;
  listSessions: (query: { created: { gte: number; lte: number }; limit: 100; starting_after?: string }) => Promise<unknown>;
  fetchSubscription: (id: string) => Promise<unknown>;
}) {
  return {
    async runOnce(): Promise<boolean> {
      return withTransaction(options.pool, async (tx) => {
        const selected = await tx.query<CheckoutAttempt & { recovery_cursor: string | null; recovery_failures: number; stripe_customer_id: string | null; account_license_id: string }>(`
          SELECT c.*,a.stripe_customer_id,a.license_id AS account_license_id FROM billing.checkout_attempts c
          JOIN billing.accounts a ON a.user_id=c.user_id WHERE c.status='creating'
            AND c.expires_at<now()-INTERVAL '1 minute' AND c.recovery_after<=now()
          ORDER BY c.recovery_after,c.created_at FOR UPDATE OF a,c SKIP LOCKED LIMIT 1`);
        const attempt = selected.rows[0];
        if (!attempt) return false;
        await tx.query("SAVEPOINT recover_checkout");
        try {
          if (attempt.license_id !== attempt.account_license_id ||
              attempt.stripe_parameters.metadata?.checkout_attempt_id !== attempt.id) {
            throw new Error("Checkout recovery requires verified creation metadata");
          }
          const page = pageSchema.parse(await options.listSessions({
            created: { gte: Math.floor(attempt.created_at.getTime() / 1000) - 60, lte: Math.floor(attempt.expires_at.getTime() / 1000) },
            limit: 100, ...(attempt.recovery_cursor ? { starting_after: attempt.recovery_cursor } : {}),
          }));
          const matches = page.data.filter((session) => session.metadata?.checkout_attempt_id === attempt.id);
          if (matches.length > 1) throw new Error("Ambiguous checkout recovery");
          const session = matches[0];
          if (session) {
            if (session.mode !== "subscription" || session.client_reference_id !== attempt.user_id ||
                session.metadata?.user_id !== attempt.user_id || session.metadata?.license_id !== attempt.license_id) {
              throw new Error("Checkout recovery identity mismatch");
            }
            const status = session.status === "complete" ? "completed" : session.status;
            if (!status || (status === "open" && !session.url)) throw new Error("Checkout recovery state is incomplete");
            if (status === "completed") {
              const subscriptionId = typeof session.subscription === "string" ? session.subscription : session.subscription?.id;
              if (!subscriptionId) throw new Error("Completed checkout has no subscription");
              await synchronizeSubscription(tx, {
                account: { user_id: attempt.user_id, license_id: attempt.account_license_id, stripe_customer_id: attempt.stripe_customer_id },
                subscriptionId, catalog: options.catalog, fetchSubscription: options.fetchSubscription,
              });
            }
            await tx.query(`UPDATE billing.checkout_attempts SET status=$2,stripe_checkout_session_id=$3,
              checkout_url=$4,expires_at=$5,recovery_cursor=NULL,recovery_failures=0,last_recovery_error=NULL,updated_at=now() WHERE id=$1`,
              [attempt.id, status, session.id, session.url ?? "", new Date(session.expires_at * 1000)]);
          } else if (page.has_more) {
            const cursor = page.data.at(-1)?.id;
            if (!cursor || cursor === attempt.recovery_cursor) throw new Error("Checkout recovery pagination did not advance");
            await tx.query("UPDATE billing.checkout_attempts SET recovery_cursor=$2,recovery_failures=0,last_recovery_error=NULL WHERE id=$1", [attempt.id, cursor]);
          } else {
            // The fixed creation window is exhausted and every possible session
            // has expired. A later user request may safely create a new attempt.
            await tx.query("UPDATE billing.checkout_attempts SET status='failed',recovery_cursor=NULL,last_recovery_error=NULL,updated_at=now() WHERE id=$1", [attempt.id]);
          }
          await tx.query("RELEASE SAVEPOINT recover_checkout");
        } catch {
          await tx.query("ROLLBACK TO SAVEPOINT recover_checkout");
          await tx.query(`UPDATE billing.checkout_attempts SET recovery_failures=recovery_failures+1,
            last_recovery_error='checkout_recovery_failed',recovery_after=now()+($2::integer*INTERVAL '1 minute') WHERE id=$1`,
            [attempt.id, Math.min(60, 2 ** Math.min(attempt.recovery_failures, 6))]);
        }
        return true;
      });
    },
  };
}
