import { createHash } from "node:crypto";
import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import type { PaymentEntitlementEvent, PaymentEvent, PurchaseReversalEvent } from "../../../../../packages/service-contracts/src/payments.js";
import { subscriptionAccessDeadline } from "./policy.js";

export class EntitlementConflict extends Error {}
export class EntitlementAccountUnavailable extends Error {}
export type ProjectionResult = "applied" | "duplicate" | "superseded";

export function createEntitlementRepository(options: {
  pool: Pool;
  applyEntitlements: (tx: PoolClient, event: PaymentEntitlementEvent) => Promise<void>;
  applyPurchaseReversal: (tx: PoolClient, event: PurchaseReversalEvent, accountActive: boolean) => Promise<void>;
}) {
  return {
    async receive(event: PaymentEvent): Promise<ProjectionResult> {
      // Hash normalized validated fields, independent of HTTP JSON whitespace.
      const payloadHash = createHash("sha256").update(JSON.stringify(event)).digest("hex");
      return withTransaction(options.pool, async (tx) => {
        const user = await tx.query<{ lifecycle_state: string }>(`SELECT lifecycle_state FROM users WHERE id=$1 AND license_id=$2 FOR UPDATE`, [event.userId, event.licenseId]);
        if (!user.rowCount) throw new EntitlementAccountUnavailable("Account is unavailable");
        const accountActive = user.rows[0]!.lifecycle_state === "active";
        const prior = await tx.query<{ event_id: string; revision: string; payload_sha256: string }>(
          "SELECT event_id,revision,payload_sha256 FROM payment_entitlement_inbox WHERE event_id=$1 OR (user_id=$2 AND revision=$3)",
          [event.eventId, event.userId, event.revision]);
        if (prior.rows.length) {
          const row = prior.rows[0];
          if (prior.rows.length !== 1 || row?.event_id !== event.eventId || row.payload_sha256 !== payloadHash) {
            throw new EntitlementConflict("Entitlement delivery conflicts with an accepted event");
          }
          return "duplicate";
        }
        await tx.query(`INSERT INTO payment_entitlement_inbox(event_id,user_id,revision,payload_sha256)
          VALUES($1,$2,$3,$4)`, [event.eventId, event.userId, event.revision, payloadHash]);
        if ("kind" in event) {
          // An irreversible purchase reversal must be applied even if a newer
          // subscription snapshot arrived first. It has its own deduplication key.
          const inserted = await tx.query(`INSERT INTO payment_purchase_reversals(purchase_id,user_id,license_id,event_id,reason)
            VALUES($1,$2,$3,$4,$5) ON CONFLICT(purchase_id) DO NOTHING`,
            [event.purchaseId, event.userId, event.licenseId, event.eventId, event.reason]);
          if (!inserted.rowCount) {
            const prior = await tx.query<{ user_id: string; license_id: string }>(
              "SELECT user_id,license_id FROM payment_purchase_reversals WHERE purchase_id=$1", [event.purchaseId]);
            if (prior.rows[0]?.user_id !== event.userId || prior.rows[0].license_id !== event.licenseId) {
              throw new EntitlementConflict("Purchase reversal identity conflicts with an accepted event");
            }
            return "duplicate";
          }
          await options.applyPurchaseReversal(tx, event, accountActive);
          return "applied";
        }
        const changed = await tx.query(`INSERT INTO payment_entitlement_projections(user_id,license_id,revision,event_id,payload,access_expires_at)
          VALUES($1,$2,$3,$4,$5::jsonb,$6) ON CONFLICT(user_id) DO UPDATE SET
          license_id=EXCLUDED.license_id,revision=EXCLUDED.revision,event_id=EXCLUDED.event_id,
          payload=EXCLUDED.payload,access_expires_at=EXCLUDED.access_expires_at,updated_at=now()
          WHERE payment_entitlement_projections.revision<EXCLUDED.revision`,
          [event.userId, event.licenseId, event.revision, event.eventId, JSON.stringify(event), accountActive ? subscriptionAccessDeadline(event.subscription) : null]);
        // Retain ordered delivery evidence for deleted/pending accounts without
        // applying subscription access or minting a fresh wallet allowance.
        if (!changed.rowCount || !accountActive) return "superseded";
        await options.applyEntitlements(tx, event);
        return "applied";
      }, { mode: "service" });
    },
  };
}
