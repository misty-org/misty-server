import type { PoolClient } from "pg";
import { enqueuePurchaseReversal } from "../entitlements/repository.js";
import { lockBillingAccount } from "../subscriptions/repository.js";

export class LegacyPurchaseImportPending extends Error {}

/** The caller owns the verified webhook's inbox-completion transaction. */
export async function reverseLegacyPurchase(tx: PoolClient, options: {
  chargeId: string;
  paymentIntentId?: string;
  reason: "refunded" | "disputed";
  eventType: string;
}) {
  const ready = await tx.query("SELECT 1 FROM billing.cutover_checkpoints WHERE name='legacy_purchases_imported'");
  if (!ready.rowCount) throw new LegacyPurchaseImportPending("Legacy purchase import has not been verified");
  const found = await tx.query<{ id: string; user_id: string; license_id: string }>(`
    SELECT id,user_id,license_id FROM billing.legacy_purchases
    WHERE stripe_charge_id=$1 OR ($2::text IS NOT NULL AND stripe_payment_intent_id=$2)`,
    [options.chargeId, options.paymentIntentId ?? null]);
  if (!found.rows.length) return false; // A subscription charge is not a lifetime purchase.
  if (found.rows.length !== 1) throw new Error("Legacy payment identifiers conflict");
  const purchase = found.rows[0]!;
  const account = await lockBillingAccount(tx, purchase.user_id);
  if (account.license_id !== purchase.license_id) throw new Error("Legacy purchase license mismatch");
  const changed = await tx.query(`UPDATE billing.legacy_purchases SET status=$2,event_source=$3,updated_at=now(),
    stripe_charge_id=COALESCE(stripe_charge_id,$4) WHERE id=$1 AND status='completed'`,
    [purchase.id, options.reason, options.eventType, options.chargeId]);
  if (!changed.rowCount) return false;
  await enqueuePurchaseReversal(tx, {
    userId: account.user_id, licenseId: account.license_id, purchaseId: purchase.id, reason: options.reason,
  });
  return true;
}
