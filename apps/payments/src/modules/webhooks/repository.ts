import { randomUUID } from "node:crypto";
import type { Pool, PoolClient } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";

export interface VerifiedWebhook {
  id: string;
  type: string;
  created: number;
  payload: unknown;
  payloadSha256: string;
}
export interface ClaimedWebhook {
  eventId: string;
  leaseId: string;
  payload: unknown;
  attempts: number;
}
export interface WebhookInbox {
  accept(event: VerifiedWebhook): Promise<void>;
}

export function createWebhookRepository(pool: Pool) {
  return {
    async accept(event: VerifiedWebhook): Promise<void> {
      // event_id is authoritative: different delivery signatures do not create new work.
      await pool.query(`INSERT INTO billing.webhook_inbox
        (event_id,event_type,stripe_created_at,payload,payload_sha256) VALUES ($1,$2,$3,$4::jsonb,$5)
        ON CONFLICT (event_id) DO NOTHING`, [event.id, event.type, event.created, JSON.stringify(event.payload), event.payloadSha256]);
    },
    async claim(): Promise<ClaimedWebhook | null> {
      const leaseId = randomUUID();
      return withTransaction(pool, async (tx) => {
        const result = await tx.query<{ event_id: string; payload: unknown; attempts: number }>(`WITH candidate AS (
          SELECT event_id FROM billing.webhook_inbox
          WHERE (state IN ('pending','failed') AND available_at<=now())
             OR (state='processing' AND lease_expires_at<=now())
          ORDER BY available_at,received_at FOR UPDATE SKIP LOCKED LIMIT 1
        ) UPDATE billing.webhook_inbox w SET state='processing',lease_id=$1,
          lease_expires_at=now()+INTERVAL '60 seconds',attempts=w.attempts+1
          FROM candidate WHERE w.event_id=candidate.event_id
          RETURNING w.event_id,w.payload,w.attempts`, [leaseId]);
        const row = result.rows[0];
        return row ? { eventId: row.event_id, leaseId, payload: row.payload, attempts: row.attempts } : null;
      });
    },
    async complete(job: ClaimedWebhook, operation: (tx: PoolClient) => Promise<void>): Promise<boolean> {
      // Subscription changes, outbox records and inbox completion share a commit.
      return withTransaction(pool, async (tx) => {
        const result = await tx.query(`SELECT event_id FROM billing.webhook_inbox
          WHERE event_id=$1 AND lease_id=$2 AND state='processing' AND lease_expires_at>now() FOR UPDATE`, [job.eventId, job.leaseId]);
        if (!result.rowCount) return false;
        await operation(tx);
        await tx.query(`UPDATE billing.webhook_inbox SET state='completed',completed_at=now(),
          lease_id=NULL,lease_expires_at=NULL,last_error_code=NULL WHERE event_id=$1`, [job.eventId]);
        return true;
      });
    },
    async retry(job: ClaimedWebhook, errorCode: string): Promise<void> {
      // Callers pass enumerated safe codes, not Stripe response bodies or exception text.
      if (!/^[a-z_]{1,64}$/.test(errorCode)) throw new Error("Invalid webhook error code");
      const delay = Math.min(3600, 2 ** Math.min(job.attempts, 12));
      await pool.query(`UPDATE billing.webhook_inbox SET state='failed',last_error_code=$3,
        available_at=now()+($4::integer*INTERVAL '1 second'),lease_id=NULL,lease_expires_at=NULL
        WHERE event_id=$1 AND lease_id=$2 AND state='processing'`, [job.eventId, job.leaseId, errorCode, delay]);
    },
  };
}
