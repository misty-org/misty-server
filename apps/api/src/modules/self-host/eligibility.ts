import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { subscriptionEntitlementEventSchema } from "../../../../../packages/service-contracts/src/payments.js";
import type { AuthUser } from "../auth/model.js";

/** API-owned license and signed payment projection; no Stripe credentials or billing-schema access. */
export function createSelfHostEligibility(pool: Pool) {
  return async (user: Pick<AuthUser, "id" | "license_id">, now: Date): Promise<Date | null> => withTransaction(pool, async (tx) => {
    const row = (await tx.query<{ status: string; expires_at: Date | null; payload: unknown }>(
      `SELECT l.status,l.expires_at,p.payload FROM users u JOIN licenses l ON l.id=u.license_id AND l.user_id=u.id
       LEFT JOIN payment_entitlement_projections p ON p.user_id=u.id AND p.license_id=l.id
       WHERE u.id=$1 AND u.license_id=$2 AND u.lifecycle_state='active'`, [user.id, user.license_id])).rows[0];
    if (!row) return null;
    if (row.status === "trialing" && row.expires_at && row.expires_at > now) return row.expires_at;
    if (!row.payload) return null;
    const event = subscriptionEntitlementEventSchema.parse(row.payload);
    if (event.userId !== user.id || event.licenseId !== user.license_id) throw new Error("Payment projection identity mismatch");
    const subscription = event.subscription;
    if (!subscription || !["active", "trialing"].includes(subscription.status) || !subscription.currentPeriodEnd) return null;
    // Offline proofs stop at the paid period boundary. The online 72-hour
    // reconciliation grace must not mint additional offline access.
    const end = new Date(subscription.currentPeriodEnd);
    return end > now ? end : null;
  }, { mode: "user", userId: user.id, licenseId: user.license_id });
}
