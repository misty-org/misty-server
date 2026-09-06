import type { PoolClient } from "pg";
import { ClosureCleanupError } from "./model.js";

const known = `SELECT 'checkout'::text AS kind,stripe_checkout_session_id AS resource_id,NULL::text AS customer_id FROM billing.checkout_attempts WHERE user_id=$1 AND stripe_checkout_session_id IS NOT NULL
  UNION SELECT 'checkout',COALESCE(stripe_checkout_session_id,source_session_id),NULL FROM billing.legacy_checkout_recovery WHERE user_id=$1 AND COALESCE(stripe_checkout_session_id,source_session_id) IS NOT NULL
  UNION SELECT 'checkout',stripe_checkout_session_id,stripe_customer_id FROM billing.legacy_purchases WHERE user_id=$1
  UNION SELECT 'subscription',stripe_subscription_id,stripe_customer_id FROM billing.subscriptions WHERE user_id=$1
  UNION SELECT 'customer',stripe_customer_id,stripe_customer_id FROM billing.accounts WHERE user_id=$1 AND stripe_customer_id IS NOT NULL
  UNION SELECT 'customer',stripe_customer_id,stripe_customer_id FROM billing.subscriptions WHERE user_id=$1
  UNION SELECT 'customer',stripe_customer_id,stripe_customer_id FROM billing.legacy_purchases WHERE user_id=$1 AND stripe_customer_id IS NOT NULL`;
export async function seedClosureResources(tx: PoolClient, userId: string) {
  if ((await tx.query(`WITH known AS (${known}) SELECT 1 FROM known GROUP BY kind,resource_id HAVING COUNT(DISTINCT customer_id)>1 LIMIT 1`, [userId])).rowCount) throw new ClosureCleanupError("closure_identity_mismatch");
  const conflict = await tx.query(`WITH known AS (${known}) SELECT 1 FROM known k JOIN billing.account_closure_resources r USING(kind,resource_id)
    WHERE r.user_id<>$1 OR (k.customer_id IS NOT NULL AND r.customer_id IS NOT NULL AND k.customer_id<>r.customer_id) LIMIT 1`, [userId]);
  if (conflict.rowCount) throw new ClosureCleanupError("closure_identity_mismatch");
  await tx.query(`WITH known AS (${known}) INSERT INTO billing.account_closure_resources(kind,resource_id,user_id,customer_id)
    SELECT kind,resource_id,$1,MAX(customer_id) FROM known GROUP BY kind,resource_id ON CONFLICT(kind,resource_id) DO NOTHING`, [userId]);
}
export async function enqueueClosureResource(tx: PoolClient, userId: string, kind: "checkout" | "subscription" | "customer", id: string, customerId: string | null) {
  const row = await tx.query(`INSERT INTO billing.account_closure_resources(kind,resource_id,user_id,customer_id) VALUES($1,$2,$3,$4)
    ON CONFLICT(kind,resource_id) DO UPDATE SET customer_id=COALESCE(account_closure_resources.customer_id,EXCLUDED.customer_id)
    WHERE account_closure_resources.user_id=EXCLUDED.user_id
      AND (account_closure_resources.customer_id IS NULL OR EXCLUDED.customer_id IS NULL OR account_closure_resources.customer_id=EXCLUDED.customer_id)
    RETURNING resource_id`, [kind, id, userId, customerId]);
  if (!row.rowCount) throw new ClosureCleanupError("closure_identity_mismatch");
}
export async function assertClosureCustomer(tx: PoolClient, userId: string, customerId: string) {
  const other = await tx.query(`SELECT 1 FROM billing.accounts WHERE stripe_customer_id=$2 AND user_id<>$1
    UNION ALL SELECT 1 FROM billing.subscriptions WHERE stripe_customer_id=$2 AND user_id<>$1
    UNION ALL SELECT 1 FROM billing.legacy_purchases WHERE stripe_customer_id=$2 AND user_id<>$1
    UNION ALL SELECT 1 FROM billing.account_closure_resources WHERE kind='customer' AND resource_id=$2 AND user_id<>$1 LIMIT 1`, [userId, customerId]);
  if (other.rowCount) throw new ClosureCleanupError("closure_identity_mismatch");
}
export async function unresolvedClosureCheckouts(tx: PoolClient, userId: string) {
  return Boolean((await tx.query(`SELECT 1 FROM billing.checkout_attempts WHERE user_id=$1 AND (status IN ('creating','open') OR (status='completed' AND stripe_checkout_session_id IS NULL))
    UNION ALL SELECT 1 FROM billing.legacy_checkout_recovery WHERE user_id=$1 AND (state<>'verified' OR status IS NULL OR status='open') LIMIT 1`, [userId])).rowCount);
}
export async function completeClosureResource(tx: PoolClient, userId: string, kind: string, id: string) {
  await tx.query("UPDATE billing.account_closure_resources SET state='completed',completed_at=now(),updated_at=now() WHERE user_id=$1 AND kind=$2 AND resource_id=$3", [userId, kind, id]);
}
export async function assertClosureResourceOwner(tx: PoolClient, userId: string, kind: string, id: string) {
  const other = kind === "checkout" ? await tx.query(`SELECT 1 FROM billing.checkout_attempts WHERE stripe_checkout_session_id=$2 AND user_id<>$1
    UNION ALL SELECT 1 FROM billing.legacy_checkout_recovery WHERE (stripe_checkout_session_id=$2 OR source_session_id=$2) AND user_id<>$1
    UNION ALL SELECT 1 FROM billing.legacy_purchases WHERE stripe_checkout_session_id=$2 AND user_id<>$1 LIMIT 1`, [userId, id]) :
    kind === "subscription" ? await tx.query("SELECT 1 FROM billing.subscriptions WHERE stripe_subscription_id=$2 AND user_id<>$1", [userId, id]) : null;
  if (other?.rowCount) throw new ClosureCleanupError("closure_identity_mismatch");
}
