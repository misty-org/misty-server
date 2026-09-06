import { randomUUID } from "node:crypto";
import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { paymentEntitlementEventSchema, subscriptionEntitlementEventSchema, purchaseReversalEventSchema,
  type PaymentEntitlementEvent, type PaymentEvent, type PurchaseReversalEvent, type SubscriptionProjection } from "../../../../../packages/service-contracts/src/payments.js";

/** Called inside the same account-locked transaction as the subscription change. */
export async function enqueueEntitlement(tx: PoolClient, options: {
  userId: string;
  licenseId: string;
  subscription: SubscriptionProjection | null;
}): Promise<PaymentEntitlementEvent> {
  const closing = (await tx.query("SELECT 1 FROM billing.account_closures WHERE user_id=$1", [options.userId])).rowCount;
  if (closing && options.subscription && !["canceled", "incomplete_expired"].includes(options.subscription.status)) {
    // A late canonical event can reveal billing created outside our admission
    // path. Reopen cleanup, never billing admission, on the retained tombstone.
    await tx.query(`UPDATE billing.account_closures SET state='closing',completed_at=NULL,
      last_error_code='late_billing_activity',available_at=now(),attempts=0,updated_at=now() WHERE user_id=$1 AND state='closed'`, [options.userId]);
  }
  const event = subscriptionEntitlementEventSchema.parse({ ...await eventIdentity(tx, options.userId), ...options,
    subscription: closing ? null : options.subscription });
  await storeEvent(tx, event);
  await tx.query("UPDATE billing.accounts SET subscription_snapshot_enqueued=true WHERE user_id=$1", [options.userId]);
  return event;
}

async function eventIdentity(tx: PoolClient, userId: string) {
  const revision = await tx.query<{ revision: string }>(`INSERT INTO billing.entitlement_versions(user_id,revision)
    VALUES($1,1) ON CONFLICT(user_id) DO UPDATE SET revision=entitlement_versions.revision+1 RETURNING revision`, [userId]);
  return {
    version: 1 as const, eventId: randomUUID(), userId,
    revision: revision.rows[0]?.revision, generatedAt: new Date().toISOString(),
  };
}

async function storeEvent(tx: PoolClient, event: PaymentEvent) {
  await tx.query(`INSERT INTO billing.entitlement_outbox(event_id,user_id,revision,payload)
    VALUES($1,$2,$3,$4::jsonb)`, [event.eventId, event.userId, event.revision, JSON.stringify(event)]);
}

export async function enqueuePurchaseReversal(tx: PoolClient, options: {
  userId: string; licenseId: string; purchaseId: string; reason: "refunded" | "disputed";
}): Promise<PurchaseReversalEvent> {
  const event = purchaseReversalEventSchema.parse({ ...await eventIdentity(tx, options.userId), ...options, kind: "purchase_reversal" });
  await storeEvent(tx, event);
  return event;
}

export interface EntitlementDelivery {
  event: PaymentEvent;
  leaseId: string;
  attempts: number;
}

export function createEntitlementOutbox(pool: Pool) {
  return {
    async claim(): Promise<EntitlementDelivery | null> {
      const leaseId = randomUUID();
      return withTransaction(pool, async (tx) => {
        const result = await tx.query<{ payload: unknown; attempts: number }>(`WITH candidate AS (
          SELECT event_id FROM billing.entitlement_outbox WHERE
          (state IN ('pending','failed') AND available_at<=now()) OR
          (state='processing' AND lease_expires_at<=now())
          ORDER BY available_at,created_at FOR UPDATE SKIP LOCKED LIMIT 1
        ) UPDATE billing.entitlement_outbox e SET state='processing',lease_id=$1,
          lease_expires_at=now()+INTERVAL '60 seconds',attempts=e.attempts+1
          FROM candidate WHERE e.event_id=candidate.event_id RETURNING e.payload,e.attempts`, [leaseId]);
        const row = result.rows[0];
        return row ? { event: paymentEntitlementEventSchema.parse(row.payload), leaseId, attempts: row.attempts } : null;
      });
    },
    async acknowledge(delivery: EntitlementDelivery): Promise<boolean> {
      const result = await pool.query(`UPDATE billing.entitlement_outbox SET state='delivered',
        delivered_at=now(),lease_id=NULL,lease_expires_at=NULL,last_error_code=NULL
        WHERE event_id=$1 AND lease_id=$2 AND state='processing'`, [delivery.event.eventId, delivery.leaseId]);
      return result.rowCount === 1;
    },
    async retry(delivery: EntitlementDelivery, errorCode: string): Promise<void> {
      if (!/^[a-z_]{1,64}$/.test(errorCode)) throw new Error("Invalid delivery error code");
      const delay = Math.min(3600, 2 ** Math.min(delivery.attempts, 12));
      await pool.query(`UPDATE billing.entitlement_outbox SET state='failed',last_error_code=$3,
        available_at=now()+($4::integer*INTERVAL '1 second'),lease_id=NULL,lease_expires_at=NULL
        WHERE event_id=$1 AND lease_id=$2 AND state='processing'`, [delivery.event.eventId, delivery.leaseId, errorCode, delay]);
    },
  };
}
