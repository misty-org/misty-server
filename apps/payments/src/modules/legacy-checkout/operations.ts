import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { lockBillingAccount } from "../subscriptions/repository.js";

/** Operator output omits customer identifiers, URLs and complete source records. */
export async function legacyCheckoutRecoveryReport(pool: Pool, after: string | null = null) {
  const groups = await pool.query<{ state: string; status: string | null; error: string | null; count: string }>(`SELECT state,status,last_recovery_error AS error,count(*)::text count
    FROM billing.legacy_checkout_recovery GROUP BY state,status,last_recovery_error ORDER BY state,status,last_recovery_error`);
  const review = await pool.query<{ id: string; error: string | null }>(`SELECT id,last_recovery_error AS error FROM billing.legacy_checkout_recovery
    WHERE state='review' AND ($1::text IS NULL OR id>$1) ORDER BY id LIMIT 100`, [after]);
  return { groups: groups.rows, review: review.rows, next: review.rows.length === 100 ? review.rows.at(-1)!.id : null };
}

/** Requeue only after an operator has investigated/corrected provider or source
 * evidence. This cannot select a session, bypass validation or create a checkout. */
export async function retryLegacyCheckoutReview(pool: Pool, id: string) {
  if (!id) throw new Error("Invalid legacy checkout attempt ID");
  return withTransaction(pool, async (tx) => {
    const target = await tx.query<{ user_id: string }>("SELECT user_id FROM billing.legacy_checkout_recovery WHERE id=$1", [id]);
    if (!target.rows[0]) throw new Error("Legacy checkout attempt not found");
    await lockBillingAccount(tx, target.rows[0].user_id);
    const changed = await tx.query(`UPDATE billing.legacy_checkout_recovery SET state='pending',recovery_cursor=NULL,
      candidate_session_id=NULL,recovery_pages=0,recovery_after=now(),recovery_failures=0,last_recovery_error=NULL
      WHERE id=$1 AND state='review'`, [id]);
    return { id, requeued: changed.rowCount === 1 };
  });
}
