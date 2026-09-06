import { z } from "zod";
import type { PoolClient } from "pg";
import type { PriceCatalog } from "../subscriptions/model.js";
import { SubscriptionValidationError } from "../subscriptions/model.js";
import { lockBillingAccount, synchronizeSubscription } from "../subscriptions/repository.js";
import type { createWebhookRepository } from "./repository.js";
import { reverseLegacyPurchase, LegacyPurchaseImportPending } from "../legacy-purchases/service.js";

const eventSchema = z.object({
  id: z.string().min(1), type: z.string().min(1),
  data: z.object({ object: z.record(z.string(), z.unknown()) }),
});
function id(value: unknown): string | undefined {
  if (typeof value === "string" && value) return value;
  const expanded = z.object({ id: z.string().min(1) }).safeParse(value);
  return expanded.success ? expanded.data.id : undefined;
}
function metadataUser(value: unknown): string | undefined {
  const metadata = z.object({ user_id: z.string().trim().min(1) }).safeParse(value);
  return metadata.success ? metadata.data.user_id : undefined;
}

async function resolveUser(tx: PoolClient, subscriptionId: string, metadata: unknown, fetchSubscription: (id: string) => Promise<unknown>) {
  const existing = await tx.query<{ user_id: string }>("SELECT user_id FROM billing.subscriptions WHERE stripe_subscription_id=$1", [subscriptionId]);
  if (existing.rows[0]) return existing.rows[0].user_id;
  const user = metadataUser(metadata);
  if (user) return user;
  const canonical = z.object({ metadata: z.unknown() }).parse(await fetchSubscription(subscriptionId));
  const discovered = metadataUser(canonical.metadata);
  if (!discovered) throw new SubscriptionValidationError("subscription_identity_mismatch");
  return discovered;
}

export function createWebhookWorker(options: {
  inbox: ReturnType<typeof createWebhookRepository>;
  catalog: PriceCatalog;
  fetchSubscription: (id: string) => Promise<unknown>;
  fetchCharge?: (id: string) => Promise<unknown>;
}) {
  return {
    async runOnce(): Promise<boolean> {
      const job = await options.inbox.claim();
      if (!job) return false;
      try {
        const event = eventSchema.parse(job.payload);
        if (event.id !== job.eventId) throw new Error("Webhook event identity mismatch");
        await options.inbox.complete(job, async (tx) => {
          const object = event.data.object;
          const attemptMetadata = z.object({ checkout_attempt_id: z.uuid() }).safeParse(object.metadata);
          const attemptId = attemptMetadata.success ? attemptMetadata.data.checkout_attempt_id : null;
          if (event.type === "charge.refunded") {
            const chargeId = id(object.id);
            if (!chargeId) throw new Error("Refund event is missing its charge");
            await reverseLegacyPurchase(tx, { chargeId, reason: "refunded", eventType: event.type,
              ...(id(object.payment_intent) ? { paymentIntentId: id(object.payment_intent)! } : {}),
            });
            return;
          }
          if (event.type === "charge.dispute.created") {
            const chargeId = id(object.charge);
            if (!chargeId) throw new Error("Dispute event is missing its charge");
            const charge = options.fetchCharge ? z.object({ id: z.string(), payment_intent: z.unknown() }).parse(await options.fetchCharge(chargeId)) : undefined;
            if (charge && charge.id !== chargeId) throw new Error("Dispute charge identity mismatch");
            await reverseLegacyPurchase(tx, { chargeId, reason: "disputed", eventType: event.type,
              ...(id(charge?.payment_intent) ? { paymentIntentId: id(charge?.payment_intent)! } : {}),
            });
            return;
          }
          let subscriptionId: string | undefined;
          if (["customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted"].includes(event.type)) {
            subscriptionId = id(object.id);
            if (!subscriptionId) throw new Error("Missing subscription ID");
          } else if (["invoice.paid", "invoice.payment_failed"].includes(event.type)) {
            const parent = z.object({ subscription_details: z.object({ subscription: z.unknown() }).nullish() }).safeParse(object.parent);
            subscriptionId = id(object.subscription) ?? (parent.success ? id(parent.data.subscription_details?.subscription) : undefined);
          } else if (["checkout.session.completed", "checkout.session.async_payment_succeeded"].includes(event.type)) {
            if (object.mode !== "subscription") return; // Retired one-time checkout never grants access.
            subscriptionId = id(object.subscription);
          } else if (event.type === "checkout.session.expired") {
            const sessionId = id(object.id);
            if (!sessionId) throw new Error("Missing checkout session ID");
            const attempt = await tx.query<{ user_id: string }>(`SELECT user_id FROM billing.checkout_attempts
              WHERE stripe_checkout_session_id=$1 OR (id=$2::uuid AND stripe_checkout_session_id IS NULL)`, [sessionId, attemptId]);
            if (attempt.rows[0]) {
              await lockBillingAccount(tx, attempt.rows[0].user_id);
              await tx.query(`UPDATE billing.checkout_attempts SET status='expired',stripe_checkout_session_id=$1,updated_at=now()
                WHERE user_id=$3 AND (stripe_checkout_session_id=$1 OR (id=$2::uuid AND stripe_checkout_session_id IS NULL))
                AND status IN ('creating','open')`, [sessionId, attemptId, attempt.rows[0].user_id]);
            }
            return;
          } else return;
          if (!subscriptionId) {
            if (event.type.startsWith("checkout.")) throw new Error("Subscription checkout is not yet resolvable");
            return; // Non-subscription invoice.
          }
          const userId = await resolveUser(tx, subscriptionId, object.metadata, options.fetchSubscription);
          const account = await lockBillingAccount(tx, userId);
          await synchronizeSubscription(tx, { account, subscriptionId, catalog: options.catalog, fetchSubscription: options.fetchSubscription });
          if (event.type.startsWith("checkout.")) {
            await tx.query(`UPDATE billing.checkout_attempts SET status='completed',stripe_checkout_session_id=$1,updated_at=now()
              WHERE (stripe_checkout_session_id=$1 OR (id=$3::uuid AND stripe_checkout_session_id IS NULL))
              AND user_id=$2 AND status IN ('creating','open','expired')`, [id(object.id), userId, attemptId]);
          }
        });
      } catch (error) {
        await options.inbox.retry(job, error instanceof SubscriptionValidationError ? error.code :
          error instanceof LegacyPurchaseImportPending ? "legacy_import_pending" : "webhook_processing_failed");
      }
      return true;
    },
  };
}
