import type { Pool, PoolClient } from "pg";
import { z } from "zod";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { recoverySessionSchema } from "../checkout/recovery.js";
import type { PriceCatalog } from "../subscriptions/model.js";
import { synchronizeSubscription } from "../subscriptions/repository.js";

const sessionSchema = recoverySessionSchema.extend({ created: z.number().int().nonnegative(),
  customer: z.union([z.string().min(1), z.object({ id: z.string().min(1) })]).nullable() });
const pageSchema = z.object({ data: z.array(sessionSchema).max(100), has_more: z.boolean() });
type Session = z.infer<typeof sessionSchema>;
interface Attempt {
  id: string; user_id: string; license_id: string; tier: string; billing_interval: string;
  source_status: string; source_session_id: string | null; source_created_at: Date; source_expires_at: Date;
  stripe_checkout_session_id: string | null; recovery_cursor: string | null; candidate_session_id: string | null;
  recovery_failures: number; recovery_pages: number; account_license_id: string; stripe_customer_id: string | null;
}
class ReviewRequired extends Error { constructor(readonly code: string) { super(code); } }
function assertSession(attempt: Attempt, session: Session, discovered: boolean) {
  const customer = typeof session.customer === "string" ? session.customer : session.customer?.id;
  if (session.mode !== "subscription" || session.client_reference_id !== attempt.user_id ||
      session.metadata?.user_id !== attempt.user_id || session.metadata.license_id !== attempt.license_id ||
      session.metadata.kind !== "subscription" || session.metadata.tier !== attempt.tier || session.metadata.interval !== attempt.billing_interval ||
      session.metadata.checkout_attempt_id || (attempt.stripe_customer_id && attempt.stripe_customer_id !== customer) || (session.status === "complete" && !customer)) {
    throw new ReviewRequired("legacy_checkout_identity_mismatch");
  }
  if (discovered && (session.expires_at !== Math.floor(attempt.source_expires_at.getTime() / 1000) ||
      session.created < Math.floor(attempt.source_created_at.getTime() / 1000) - 60 || session.created > Math.floor(attempt.source_expires_at.getTime() / 1000))) {
    throw new ReviewRequired("legacy_checkout_window_mismatch");
  }
  if (!session.status || (session.status === "open" && (!session.url || session.expires_at * 1000 <= Date.now()))) {
    throw new Error("Canonical checkout state is not yet resolved");
  }
}
async function assertUnclaimed(tx: PoolClient, attempt: Attempt, sessionId: string, discovered: boolean) {
  const collision = await tx.query(`SELECT 1 FROM billing.checkout_attempts WHERE stripe_checkout_session_id=$1
    UNION ALL SELECT 1 FROM billing.legacy_checkout_recovery WHERE id<>$2 AND
      (source_session_id=$1 OR stripe_checkout_session_id=$1 OR candidate_session_id=$1 OR
       (user_id=$4 AND state='verified' AND status='open') OR
       ($3 AND user_id=$4 AND source_expires_at=$5)) LIMIT 1`,
    [sessionId, attempt.id, discovered, attempt.user_id, attempt.source_expires_at]);
  if (collision.rowCount) throw new ReviewRequired("legacy_checkout_ambiguous");
}

/** Read-only Stripe recovery. One account-locked bounded page per run; known
 * sessions use canonical retrieval. No old or new checkout creation is exposed. */
export function createLegacyCheckoutRecovery(options: {
  pool: Pool; catalog: PriceCatalog;
  listSessions: (query: { created: { gte: number; lte: number }; limit: 100; starting_after?: string }) => Promise<unknown>;
  retrieveSession: (id: string) => Promise<unknown>;
  fetchSubscription: (id: string) => Promise<unknown>;
}) {
  async function apply(tx: PoolClient, attempt: Attempt, session: Session, discovered: boolean) {
    assertSession(attempt, session, discovered);
    await assertUnclaimed(tx, attempt, session.id, discovered);
    const subscriptionId = typeof session.subscription === "string" ? session.subscription : session.subscription?.id;
    if (session.status === "complete" && !subscriptionId) throw new Error("Completed checkout has no subscription");
    if (subscriptionId) {
      await synchronizeSubscription(tx, { account: { user_id: attempt.user_id, license_id: attempt.account_license_id, stripe_customer_id: attempt.stripe_customer_id },
        subscriptionId, catalog: options.catalog, fetchSubscription: options.fetchSubscription });
    }
    await tx.query(`UPDATE billing.legacy_checkout_recovery SET state='verified',status=$2,stripe_checkout_session_id=$3,
      checkout_url=$4,session_expires_at=$5,verified_at=now(),recovery_after=now()+INTERVAL '1 minute',recovery_cursor=NULL,
      candidate_session_id=NULL,recovery_failures=0,last_recovery_error=NULL WHERE id=$1`,
      [attempt.id, session.status === "complete" ? "completed" : session.status, session.id, session.url ?? "", new Date(session.expires_at * 1000)]);
  }
  return {
    async runOnce(): Promise<boolean> {
      return withTransaction(options.pool, async (tx) => {
        const ready = await tx.query("SELECT 1 FROM billing.cutover_checkpoints WHERE name='legacy_checkouts_imported'");
        if (!ready.rowCount) return false;
        const selected = await tx.query<Attempt>(`SELECT r.*,a.license_id AS account_license_id,a.stripe_customer_id
          FROM billing.legacy_checkout_recovery r JOIN billing.accounts a ON a.user_id=r.user_id
          WHERE (r.state='pending' OR (r.state='verified' AND r.status='open')) AND r.recovery_after<=now()
            AND (r.source_session_id IS NOT NULL OR r.stripe_checkout_session_id IS NOT NULL OR r.source_expires_at<now()-INTERVAL '1 minute')
          ORDER BY r.recovery_after,r.source_created_at,r.id FOR UPDATE OF a,r SKIP LOCKED LIMIT 1`);
        const attempt = selected.rows[0];
        if (!attempt) return false;
        await tx.query("SAVEPOINT legacy_checkout_recovery");
        try {
          if (attempt.license_id !== attempt.account_license_id) throw new ReviewRequired("legacy_checkout_identity_mismatch");
          const known = attempt.source_session_id ?? attempt.stripe_checkout_session_id;
          if (known) {
            const session = sessionSchema.parse(await options.retrieveSession(known));
            if (session.id !== known) throw new ReviewRequired("legacy_checkout_identity_mismatch");
            await apply(tx, attempt, session, !attempt.source_session_id);
          } else {
            if (attempt.recovery_pages >= 10000) throw new ReviewRequired("legacy_checkout_search_limit");
            const page = pageSchema.parse(await options.listSessions({ created: { gte: Math.floor(attempt.source_created_at.getTime() / 1000) - 60,
              lte: Math.floor(attempt.source_expires_at.getTime() / 1000) }, limit: 100,
              ...(attempt.recovery_cursor ? { starting_after: attempt.recovery_cursor } : {}) }));
            let candidate = attempt.candidate_session_id;
            for (const session of page.data) {
              // New native sessions carry their own persisted attempt ID and
              // cannot belong to a Go attempt. Any other apparent identity
              // match must be validated rather than silently discarded.
              if (session.metadata?.checkout_attempt_id || (session.client_reference_id !== attempt.user_id && session.metadata?.user_id !== attempt.user_id)) continue;
              assertSession(attempt, session, true);
              await assertUnclaimed(tx, attempt, session.id, true);
              if (candidate && candidate !== session.id) throw new ReviewRequired("legacy_checkout_ambiguous");
              candidate = session.id;
            }
            if (page.has_more) {
              const cursor = page.data.at(-1)?.id;
              if (!cursor || cursor === attempt.recovery_cursor) throw new ReviewRequired("legacy_checkout_invalid_pagination");
              await tx.query(`UPDATE billing.legacy_checkout_recovery SET recovery_cursor=$2,candidate_session_id=$3,
                recovery_pages=recovery_pages+1,recovery_failures=0,last_recovery_error=NULL WHERE id=$1`, [attempt.id, cursor, candidate]);
            } else if (candidate) {
              // Finish the entire window before adopting a candidate, then read
              // it again: status may have changed since an earlier listing page.
              const session = sessionSchema.parse(await options.retrieveSession(candidate));
              if (session.id !== candidate) throw new ReviewRequired("legacy_checkout_identity_mismatch");
              await apply(tx, attempt, session, true);
            } else {
              if (attempt.source_status === "completed" || attempt.source_status === "open") throw new ReviewRequired("legacy_checkout_missing_session");
              await tx.query(`UPDATE billing.legacy_checkout_recovery SET state='verified',status='absent',verified_at=now(),
                recovery_cursor=NULL,recovery_failures=0,last_recovery_error=NULL WHERE id=$1`, [attempt.id]);
            }
          }
          await tx.query("RELEASE SAVEPOINT legacy_checkout_recovery");
        } catch (error) {
          await tx.query("ROLLBACK TO SAVEPOINT legacy_checkout_recovery");
          await tx.query(`UPDATE billing.legacy_checkout_recovery SET state=CASE WHEN $2 THEN 'review' ELSE state END,
            recovery_failures=recovery_failures+1,last_recovery_error=$3,recovery_after=now()+($4::integer*INTERVAL '1 minute') WHERE id=$1`,
            [attempt.id, error instanceof ReviewRequired, error instanceof ReviewRequired ? error.code : "legacy_checkout_unavailable", Math.min(60, 2 ** Math.min(attempt.recovery_failures, 6))]);
        }
        return true;
      });
    },
  };
}
