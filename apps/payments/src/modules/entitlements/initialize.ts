import type { Pool, PoolClient, QueryResult } from "pg";
import { normalizePlan, subscriptionLicenseState } from "../../../../../packages/entitlements/src/subscription-policy.js";
import { verifyLegacySubscriptionImport } from "../subscriptions/import.js";
import { effectiveSubscription } from "../subscriptions/projection.js";
import { enqueueEntitlement } from "./repository.js";

export class EntitlementInitializationConflict extends Error {}
interface Account {
  user_id: string; license_id: string; subscription_snapshot_enqueued: boolean;
  lifecycle_state: string | null; identity_matches: boolean | null;
  tier: string | null; status: string | null; expires_at: Date | null; legacy_tier: string | null;
}

async function deliveryReport(tx: PoolClient) {
  // Cross-schema reads exist only in this migration-role command. Runtime
  // payments credentials cannot inspect the API inbox or license tables.
  return (await tx.query<{ awaitingAcknowledgement: string; awaitingProjection: string; missingDeliveryEvidence: string }>(`SELECT
    count(*) FILTER(WHERE e.event_id IS NOT NULL AND e.state<>'delivered')::text AS "awaitingAcknowledgement",
    count(*) FILTER(WHERE e.event_id IS NOT NULL AND (p.user_id IS NULL OR p.license_id<>a.license_id OR p.revision<e.revision))::text AS "awaitingProjection",
    count(*) FILTER(WHERE e.event_id IS NULL)::text AS "missingDeliveryEvidence"
    FROM billing.accounts a
    LEFT JOIN LATERAL(SELECT event_id,revision,state FROM billing.entitlement_outbox WHERE user_id=a.user_id AND payload ? 'subscription'
      ORDER BY revision DESC LIMIT 1) e ON true
    LEFT JOIN public.payment_entitlement_projections p ON p.user_id=a.user_id
    WHERE a.subscription_snapshot_enqueued AND EXISTS(SELECT 1 FROM billing.subscriptions WHERE user_id=a.user_id)`)).rows[0]!;
}

/** Before ownership switches, with every Go/native billing and license writer
 * stopped. Dry run is read-only. Commit rejects all license mismatches atomically;
 * it only queues normal signed delivery, never writes the application projection.
 */
export async function initializeSubscriptionEntitlements(pool: Pool, options: { commit: boolean }) {
  const tx = await pool.connect(); let discard = false;
  try {
    await tx.query(options.commit ? "BEGIN" : "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY");
    await tx.query("SELECT set_config('app.rls_mode','service',true)");
    if (options.commit) {
      await tx.query("SET LOCAL lock_timeout='5s'");
      await tx.query("SELECT pg_advisory_xact_lock(824717317)");
      await tx.query("LOCK TABLE public.users,public.licenses,public.stripe_purchases,public.stripe_subscriptions,public.payment_entitlement_projections IN SHARE MODE");
      await tx.query("LOCK TABLE billing.accounts,billing.subscriptions,billing.legacy_purchases,billing.legacy_subscription_records,billing.cutover_checkpoints,billing.entitlement_outbox,billing.entitlement_versions IN SHARE ROW EXCLUSIVE MODE");
    }
    await verifyLegacySubscriptionImport(tx);
    const now = (await tx.query<{ now: Date }>("SELECT transaction_timestamp() AS now")).rows[0]!.now;
    let cursor: string | null = null;
    let candidates = 0n, enqueued = 0n, conflicts = 0n, deferredInactive = 0n, alreadyEnqueued = 0n;
    for (;;) {
      const page: QueryResult<Account> = await tx.query<Account>(`SELECT a.user_id,a.license_id,a.subscription_snapshot_enqueued,u.lifecycle_state,
        (u.license_id=a.license_id AND l.user_id=a.user_id) AS identity_matches,l.tier,l.status,l.expires_at,l.legacy_tier
        FROM billing.accounts a LEFT JOIN public.users u ON u.id=a.user_id LEFT JOIN public.licenses l ON l.id=a.license_id
        WHERE ($1::text IS NULL OR a.user_id>$1) AND EXISTS(SELECT 1 FROM billing.subscriptions WHERE user_id=a.user_id)
        ORDER BY a.user_id LIMIT 100`, [cursor]);
      if (!page.rowCount) break;
      for (const account of page.rows) {
        cursor = account.user_id;
        if (!account.identity_matches) { conflicts++; continue; }
        if (account.lifecycle_state !== "active") { deferredInactive++; continue; }
        if (account.subscription_snapshot_enqueued) { alreadyEnqueued++; continue; }
        const subscription = await effectiveSubscription(tx, account.user_id);
        const expected = subscriptionLicenseState(subscription, account.legacy_tier, now);
        if (!subscription || normalizePlan(account.tier) !== expected.tier || account.status !== expected.status ||
            account.expires_at?.getTime() !== expected.expiresAt?.getTime()) { conflicts++; continue; }
        candidates++;
        if (options.commit) {
          await enqueueEntitlement(tx, { userId: account.user_id, licenseId: account.license_id, subscription });
          enqueued++;
        }
      }
    }
    if (options.commit && conflicts) throw new EntitlementInitializationConflict(`${conflicts} account license states require review; no initial snapshots were committed`);
    const delivery = await deliveryReport(tx);
    await tx.query("COMMIT");
    return { committed: options.commit, candidates: String(candidates), enqueued: String(enqueued), conflicts: String(conflicts),
      deferredInactive: String(deferredInactive), alreadyEnqueued: String(alreadyEnqueued), ...delivery };
  } catch (error) { await tx.query("ROLLBACK").catch(() => { discard = true; }); throw error; }
  finally { tx.release(discard); }
}
