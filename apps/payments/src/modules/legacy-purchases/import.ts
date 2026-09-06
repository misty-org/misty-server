import type { Pool, PoolClient } from "pg";

const checkpoint = "legacy_purchases_imported";
const columns = "id,user_id,license_id,tier_purchased,stripe_checkout_session_id,stripe_payment_intent_id,stripe_customer_id,stripe_charge_id,amount,currency,status,event_source,created_at,updated_at";

export class LegacyImportConflict extends Error {}

async function inspect(tx: PoolClient) {
  const source = await tx.query<{ purchases: string; accounts: string }>(
    "SELECT count(*)::text purchases,count(DISTINCT user_id)::text accounts FROM public.stripe_purchases");
  const invalid = await tx.query(`SELECT 1 FROM public.stripe_purchases p
    LEFT JOIN public.users u ON u.id=p.user_id LEFT JOIN public.licenses l ON l.id=p.license_id
    WHERE u.id IS NULL OR l.id IS NULL OR u.license_id<>p.license_id OR l.user_id<>p.user_id
      OR p.status NOT IN ('completed','refunded','disputed') LIMIT 1`);
  if (invalid.rowCount) throw new LegacyImportConflict("Legacy purchase identity or status requires review");
  const accounts = await tx.query(`SELECT 1 FROM public.stripe_purchases p JOIN billing.accounts a USING(user_id)
    WHERE a.license_id<>p.license_id LIMIT 1`);
  if (accounts.rowCount) throw new LegacyImportConflict("Imported account license conflicts with the source");
  const changed = await tx.query(`SELECT 1 FROM public.stripe_purchases p JOIN billing.legacy_purchases b ON
    b.id=p.id OR b.stripe_checkout_session_id=p.stripe_checkout_session_id
    OR b.stripe_payment_intent_id=p.stripe_payment_intent_id OR b.stripe_charge_id=p.stripe_charge_id
    WHERE to_jsonb(p) IS DISTINCT FROM to_jsonb(b) LIMIT 1`);
  if (changed.rowCount) throw new LegacyImportConflict("Imported purchase differs from the source; no records were overwritten");
  return source.rows[0]!;
}

/** Operator-only import on the shared database. Never called by the runtime role.
 * Go billing writers must be stopped before commit and remain stopped afterward.
 * This preserves purchases verbatim; license grants and customer selection belong
 * to their own reviewed imports and are deliberately not inferred from old tiers.
 */
export async function importLegacyPurchases(pool: Pool, options: { commit: boolean }) {
  const tx = await pool.connect();
  let discard = false;
  try {
    await tx.query(options.commit ? "BEGIN" : "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY");
    await tx.query("SELECT set_config('app.rls_mode','service',true)");
    if (options.commit) {
      await tx.query("SET LOCAL lock_timeout='5s'");
      await tx.query("SELECT pg_advisory_xact_lock(824717317)");
      await tx.query("LOCK TABLE public.users,public.licenses,public.stripe_purchases IN SHARE MODE");
      await tx.query("LOCK TABLE billing.accounts,billing.legacy_purchases,billing.cutover_checkpoints IN SHARE ROW EXCLUSIVE MODE");
    }
    const report = await inspect(tx);
    const previous = await tx.query("SELECT 1 FROM billing.cutover_checkpoints WHERE name=$1", [checkpoint]);
    if (previous.rowCount) {
      const missing = await tx.query("SELECT 1 FROM public.stripe_purchases p LEFT JOIN billing.legacy_purchases b USING(id) WHERE b.id IS NULL LIMIT 1");
      if (missing.rowCount) throw new LegacyImportConflict("Source changed after the verified import");
    } else if (options.commit) {
      await tx.query(`INSERT INTO billing.accounts(user_id,license_id)
        SELECT DISTINCT user_id,license_id FROM public.stripe_purchases ON CONFLICT(user_id) DO NOTHING`);
      await tx.query(`INSERT INTO billing.legacy_purchases(${columns}) SELECT ${columns} FROM public.stripe_purchases ON CONFLICT(id) DO NOTHING`);
      await inspect(tx);
      const missing = await tx.query("SELECT 1 FROM public.stripe_purchases p LEFT JOIN billing.legacy_purchases b USING(id) WHERE b.id IS NULL LIMIT 1");
      if (missing.rowCount) throw new LegacyImportConflict("Import verification failed");
      await tx.query("INSERT INTO billing.cutover_checkpoints(name,evidence) VALUES($1,$2::jsonb)", [checkpoint, JSON.stringify({ version: 1, ...report })]);
    }
    await tx.query("COMMIT");
    return { ...report, committed: options.commit, previouslyImported: Boolean(previous.rowCount) };
  } catch (error) {
    await tx.query("ROLLBACK").catch(() => { discard = true; });
    throw error;
  } finally {
    tx.release(discard);
  }
}
