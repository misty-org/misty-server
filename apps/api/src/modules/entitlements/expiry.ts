import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { subscriptionEntitlementEventSchema } from "../../../../../packages/service-contracts/src/payments.js";
import { createEntitlementEffects } from "./service.js";

/** Bounded API-owned expiry. Payments retains canonical subscription state and
 * continues blocking duplicate checkout even while local access has expired. */
export function createEntitlementExpiry(options: { pool: Pool; now?: () => Date }) {
  const clock = options.now ?? (() => new Date());
  return {
    async runOnce(): Promise<boolean> {
      const now = clock();
      return withTransaction(options.pool, async (tx) => {
        const found = await tx.query<{ user_id: string }>(`SELECT u.id user_id FROM users u
          JOIN licenses l ON l.id=u.license_id JOIN payment_entitlement_projections p ON p.user_id=u.id
          WHERE u.lifecycle_state='active' AND p.license_id=l.id AND p.access_expires_at<=$1
          ORDER BY p.access_expires_at,u.id FOR UPDATE OF u SKIP LOCKED LIMIT 1`, [now]);
        if (!found.rows[0]) return false;
        // Re-read after acquiring the account lock: a newer projection could
        // have committed after the candidate SELECT took its MVCC snapshot.
        const current = await tx.query<{ payload: unknown }>(`SELECT p.payload FROM payment_entitlement_projections p
          JOIN users u ON u.id=p.user_id AND u.license_id=p.license_id WHERE p.user_id=$1 AND p.access_expires_at<=$2`, [found.rows[0].user_id, now]);
        if (!current.rows[0]) return false;
        const event = subscriptionEntitlementEventSchema.parse(current.rows[0].payload);
        await createEntitlementEffects({ now: () => now }).applyEntitlements(tx, event, `expiry:${event.eventId}`);
        await tx.query("UPDATE payment_entitlement_projections SET access_expires_at=NULL WHERE user_id=$1 AND event_id=$2", [event.userId, event.eventId]);
        return true;
      }, { mode: "service" });
    },
  };
}
