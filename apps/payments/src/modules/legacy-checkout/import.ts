import type { Pool, PoolClient } from "pg";
import { verifyLegacySubscriptionImport } from "../subscriptions/import.js";

const checkpoint = "legacy_checkouts_imported";
export class LegacyCheckoutImportConflict extends Error {}
async function inspect(tx: PoolClient) {
  const invalid = await tx.query(`SELECT 1 FROM public.stripe_subscription_checkout_attempts c
    LEFT JOIN public.users u ON u.id=c.user_id LEFT JOIN public.licenses l ON l.id=c.license_id
    LEFT JOIN billing.accounts a ON a.user_id=c.user_id
    WHERE u.id IS NULL OR l.id IS NULL OR u.license_id<>c.license_id OR l.user_id<>c.user_id
      OR (a.user_id IS NOT NULL AND a.license_id<>c.license_id) OR c.id='' OR c.stripe_checkout_session_id=''
      OR c.expires_at<=c.created_at OR NOT isfinite(c.expires_at) OR NOT isfinite(c.created_at) LIMIT 1`);
  if (invalid.rowCount) throw new LegacyCheckoutImportConflict("Legacy checkout identity or creation window requires review");
  const collision = await tx.query(`SELECT 1 FROM public.stripe_subscription_checkout_attempts c JOIN billing.checkout_attempts n
    ON c.id=n.id::text OR c.stripe_checkout_session_id=n.stripe_checkout_session_id
      OR (c.user_id=n.user_id AND n.status IN ('creating','open'))
    UNION ALL SELECT 1 FROM public.stripe_subscription_checkout_attempts c JOIN billing.legacy_checkout_records r USING(id)
    WHERE to_jsonb(c) IS DISTINCT FROM r.source_record LIMIT 1`);
  if (collision.rowCount) throw new LegacyCheckoutImportConflict("Legacy checkout conflicts with native data or its original snapshot");
  return (await tx.query<{ attempts: string; accounts: string; missingSessionIds: string }>(`SELECT count(*)::text attempts,
    count(DISTINCT user_id)::text accounts,count(*) FILTER(WHERE stripe_checkout_session_id IS NULL)::text AS "missingSessionIds"
    FROM public.stripe_subscription_checkout_attempts`)).rows[0]!;
}
async function verify(tx: PoolClient) {
  const changed = await tx.query(`SELECT 1 FROM public.stripe_subscription_checkout_attempts c
    FULL JOIN billing.legacy_checkout_records r USING(id) LEFT JOIN billing.legacy_checkout_recovery b ON b.id=r.id
    WHERE c.id IS NULL OR r.id IS NULL OR b.id IS NULL OR to_jsonb(c) IS DISTINCT FROM r.source_record
      OR ROW(c.user_id,c.license_id,c.tier,c.billing_interval,c.status,c.stripe_checkout_session_id,c.created_at,c.expires_at)
        IS DISTINCT FROM ROW(b.user_id,b.license_id,b.tier,b.billing_interval,b.source_status,b.source_session_id,b.source_created_at,b.source_expires_at) LIMIT 1`);
  if (changed.rowCount) throw new LegacyCheckoutImportConflict("Legacy checkout source or recovery identity changed after import");
}

/** Operator-only, before any Go/native billing writer resumes. */
export async function importLegacyCheckouts(pool: Pool, options: { commit: boolean }) {
  const tx = await pool.connect(); let discard = false;
  try {
    await tx.query(options.commit ? "BEGIN" : "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY");
    await tx.query("SELECT set_config('app.rls_mode','service',true)");
    if (options.commit) {
      await tx.query("SET LOCAL lock_timeout='5s'");
      await tx.query("SELECT pg_advisory_xact_lock(824717317)");
      await tx.query("LOCK TABLE public.users,public.licenses,public.stripe_purchases,public.stripe_subscriptions,public.stripe_subscription_checkout_attempts IN SHARE MODE");
      await tx.query("LOCK TABLE billing.accounts,billing.subscriptions,billing.legacy_purchases,billing.legacy_subscription_records,billing.cutover_checkpoints,billing.checkout_attempts,billing.legacy_checkout_records,billing.legacy_checkout_recovery IN SHARE ROW EXCLUSIVE MODE");
    }
    await verifyLegacySubscriptionImport(tx);
    const report = await inspect(tx);
    const prior = await tx.query("SELECT 1 FROM billing.cutover_checkpoints WHERE name=$1", [checkpoint]);
    if (!prior.rowCount && options.commit) {
      await tx.query(`INSERT INTO billing.accounts(user_id,license_id) SELECT DISTINCT user_id,license_id
        FROM public.stripe_subscription_checkout_attempts ON CONFLICT(user_id) DO NOTHING`);
      await tx.query(`INSERT INTO billing.legacy_checkout_records(id,source_record)
        SELECT id,to_jsonb(c) FROM public.stripe_subscription_checkout_attempts c ON CONFLICT(id) DO NOTHING`);
      await tx.query(`INSERT INTO billing.legacy_checkout_recovery(id,user_id,license_id,tier,billing_interval,source_status,source_session_id,source_created_at,source_expires_at)
        SELECT id,user_id,license_id,tier,billing_interval,status,stripe_checkout_session_id,created_at,expires_at
        FROM public.stripe_subscription_checkout_attempts ON CONFLICT(id) DO NOTHING`);
      await verify(tx);
      await tx.query("INSERT INTO billing.cutover_checkpoints(name,evidence) VALUES($1,$2)", [checkpoint, JSON.stringify({ version: 1, ...report })]);
    } else if (prior.rowCount) await verify(tx);
    await tx.query("COMMIT"); return { ...report, committed: options.commit, previouslyImported: Boolean(prior.rowCount) };
  } catch (error) { await tx.query("ROLLBACK").catch(() => { discard = true; }); throw error; }
  finally { tx.release(discard); }
}
