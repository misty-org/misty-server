import type { Pool, PoolClient } from "pg";

const checkpoint = "legacy_subscriptions_imported";
const columns = ["stripe_subscription_id", "user_id", "stripe_customer_id", "stripe_price_id", "tier", "billing_interval", "status",
  "current_period_end", "cancel_at_period_end", "canceled_at", "source_event_id", "source_event_created_at", "reconcile_after",
  "last_reconciled_at", "updated_at", "reconcile_failures", "last_reconcile_error"];
const source = `(SELECT ${columns.map((column) => column === "source_event_created_at" ? "EXTRACT(EPOCH FROM source_event_created_at)::bigint AS source_event_created_at" : column).join(",")} FROM public.stripe_subscriptions)`;
// Go prefers the effective subscription's customer, then the latest purchase.
// Stable IDs settle timestamp ties for which Go provided no further ordering.
const accounts = `(SELECT u.id AS user_id,u.license_id,COALESCE(NULLIF(s.stripe_customer_id,''),NULLIF(p.stripe_customer_id,'')) AS stripe_customer_id
  FROM public.users u LEFT JOIN LATERAL(SELECT stripe_customer_id FROM public.stripe_subscriptions WHERE user_id=u.id
    ORDER BY CASE WHEN status IN ('active','trialing') THEN 0 WHEN status='past_due' THEN 1 ELSE 2 END,updated_at DESC,stripe_subscription_id LIMIT 1) s ON true
  LEFT JOIN LATERAL(SELECT stripe_customer_id FROM public.stripe_purchases WHERE user_id=u.id AND stripe_customer_id IS NOT NULL
    ORDER BY updated_at DESC,id LIMIT 1) p ON true
  WHERE EXISTS(SELECT 1 FROM public.stripe_subscriptions WHERE user_id=u.id) OR EXISTS(SELECT 1 FROM public.stripe_purchases WHERE user_id=u.id))`;
export class SubscriptionImportConflict extends Error {}
async function inspect(tx: PoolClient) {
  const complete = await tx.query("SELECT 1 FROM billing.cutover_checkpoints WHERE name='legacy_purchases_imported'");
  if (!complete.rowCount) throw new SubscriptionImportConflict("Verify the legacy purchase import first");
  const purchaseDrift = await tx.query(`SELECT 1 FROM public.stripe_purchases p FULL JOIN billing.legacy_purchases b USING(id)
    WHERE p.id IS NULL OR b.id IS NULL OR to_jsonb(p) IS DISTINCT FROM to_jsonb(b) LIMIT 1`);
  if (purchaseDrift.rowCount) throw new SubscriptionImportConflict("Legacy purchase history changed after its verified import");
  const invalid = await tx.query(`SELECT 1 FROM public.stripe_subscriptions s
    LEFT JOIN public.users u ON u.id=s.user_id LEFT JOIN public.licenses l ON l.id=s.license_id
    WHERE u.id IS NULL OR l.id IS NULL OR u.license_id<>s.license_id OR l.user_id<>s.user_id
      OR s.tier NOT IN ('pro','max') OR s.billing_interval NOT IN ('month','year')
      OR s.status NOT IN ('incomplete','incomplete_expired','trialing','active','past_due','canceled','unpaid','paused')
      OR s.stripe_subscription_id='' OR s.stripe_customer_id='' OR s.stripe_price_id=''
      OR (s.status IN ('active','trialing') AND s.current_period_end IS NULL)
      OR EXTRACT(EPOCH FROM s.source_event_created_at)<>trunc(EXTRACT(EPOCH FROM s.source_event_created_at)) LIMIT 1`);
  if (invalid.rowCount) throw new SubscriptionImportConflict("Legacy subscription identity or state requires review");
  const customers = await tx.query(`SELECT 1 FROM ${accounts} a JOIN ${accounts} b
    ON a.stripe_customer_id=b.stripe_customer_id AND a.user_id<>b.user_id LIMIT 1`);
  if (customers.rowCount) throw new SubscriptionImportConflict("Legacy customer belongs to multiple accounts");
  const conflictingAccount = await tx.query(`SELECT 1 FROM ${accounts} a JOIN billing.accounts b
    ON a.user_id=b.user_id OR a.stripe_customer_id=b.stripe_customer_id
    WHERE a.user_id<>b.user_id OR a.license_id<>b.license_id OR (b.stripe_customer_id IS NOT NULL AND b.stripe_customer_id IS DISTINCT FROM a.stripe_customer_id) LIMIT 1`);
  if (conflictingAccount.rowCount) throw new SubscriptionImportConflict("Billing account conflicts with legacy identity or customer selection");
  const changed = await tx.query(`SELECT 1 FROM ${source} s JOIN billing.subscriptions b USING(stripe_subscription_id)
    WHERE ROW(${columns.map((column) => `s.${column}`).join(",")}) IS DISTINCT FROM ROW(${columns.map((column) => `b.${column}`).join(",")}) LIMIT 1`);
  if (changed.rowCount) throw new SubscriptionImportConflict("Existing billing subscription differs from the source");
  const archived = await tx.query(`SELECT 1 FROM public.stripe_subscriptions s JOIN billing.legacy_subscription_records b USING(stripe_subscription_id)
    WHERE to_jsonb(s) IS DISTINCT FROM b.source_record LIMIT 1`);
  if (archived.rowCount) throw new SubscriptionImportConflict("Legacy subscription changed after its snapshot");
  return (await tx.query<{ subscriptions: string; accounts: string; customer_mappings: string }>(`SELECT
    (SELECT count(*)::text FROM public.stripe_subscriptions) AS subscriptions,
    count(*)::text AS accounts,count(stripe_customer_id)::text AS customer_mappings FROM ${accounts} a`)).rows[0]!;
}
async function verifyCopied(tx: PoolClient) {
  const missing = await tx.query(`SELECT 1 FROM public.stripe_subscriptions s
    LEFT JOIN billing.subscriptions b USING(stripe_subscription_id) LEFT JOIN billing.legacy_subscription_records r USING(stripe_subscription_id)
    WHERE b.stripe_subscription_id IS NULL OR r.stripe_subscription_id IS NULL
    UNION ALL SELECT 1 FROM billing.legacy_subscription_records r LEFT JOIN public.stripe_subscriptions s USING(stripe_subscription_id) WHERE s.stripe_subscription_id IS NULL
    UNION ALL SELECT 1 FROM ${accounts} a LEFT JOIN billing.accounts b USING(user_id)
      WHERE b.user_id IS NULL OR a.license_id<>b.license_id OR a.stripe_customer_id IS DISTINCT FROM b.stripe_customer_id LIMIT 1`);
  if (missing.rowCount) throw new SubscriptionImportConflict("Subscription import verification failed or source changed");
}

/** Operator-only verification under the caller's source/target cutover locks. */
export async function verifyLegacySubscriptionImport(tx: PoolClient) {
  const complete = await tx.query("SELECT 1 FROM billing.cutover_checkpoints WHERE name=$1", [checkpoint]);
  if (!complete.rowCount) throw new SubscriptionImportConflict("Verify the legacy subscription import first");
  await inspect(tx);
  await verifyCopied(tx);
}

/** Run with migration credentials while ALL Go/native billing writers are stopped.
 * No provider calls, grants or checkout replay occur in this import.
 */
export async function importLegacySubscriptions(pool: Pool, options: { commit: boolean }) {
  const tx = await pool.connect(); let discard = false;
  try {
    await tx.query(options.commit ? "BEGIN" : "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY");
    await tx.query("SELECT set_config('app.rls_mode','service',true)");
    if (options.commit) {
      await tx.query("SET LOCAL lock_timeout='5s'");
      await tx.query("SELECT pg_advisory_xact_lock(824717317)");
      await tx.query("LOCK TABLE public.users,public.licenses,public.stripe_purchases,public.stripe_subscriptions IN SHARE MODE");
      await tx.query("LOCK TABLE billing.accounts,billing.subscriptions,billing.legacy_purchases,billing.legacy_subscription_records,billing.cutover_checkpoints IN SHARE ROW EXCLUSIVE MODE");
    }
    const report = await inspect(tx);
    const previous = await tx.query("SELECT 1 FROM billing.cutover_checkpoints WHERE name=$1", [checkpoint]);
    if (previous.rowCount) await verifyCopied(tx);
    else if (options.commit) {
      await tx.query(`INSERT INTO billing.accounts(user_id,license_id,stripe_customer_id)
        SELECT user_id,license_id,stripe_customer_id FROM ${accounts} a ON CONFLICT(user_id) DO UPDATE SET stripe_customer_id=EXCLUDED.stripe_customer_id,updated_at=now()`);
      await tx.query(`INSERT INTO billing.subscriptions(${columns.join(",")}) SELECT ${columns.join(",")} FROM ${source} s ON CONFLICT(stripe_subscription_id) DO NOTHING`);
      await tx.query(`INSERT INTO billing.legacy_subscription_records(stripe_subscription_id,source_record)
        SELECT stripe_subscription_id,to_jsonb(s) FROM public.stripe_subscriptions s ON CONFLICT(stripe_subscription_id) DO NOTHING`);
      await inspect(tx); await verifyCopied(tx);
      await tx.query("INSERT INTO billing.cutover_checkpoints(name,evidence) VALUES($1,$2)", [checkpoint, JSON.stringify({ version: 1, ...report })]);
    }
    await tx.query("COMMIT"); return { ...report, committed: options.commit, previouslyImported: Boolean(previous.rowCount) };
  } catch (error) { await tx.query("ROLLBACK").catch(() => { discard = true; }); throw error; }
  finally { tx.release(discard); }
}
