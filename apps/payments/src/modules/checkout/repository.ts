import { randomUUID } from "node:crypto";
import type { Pool } from "pg";
import type Stripe from "stripe";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import type { CheckoutIntent } from "../../../../../packages/service-contracts/src/payments.js";
import { lockBillingAccount, synchronizeSubscription } from "../subscriptions/repository.js";
import { normalizeSubscription, type PriceCatalog } from "../subscriptions/model.js";
import { assertBillingAccountOpen } from "./admission.js";
import { CheckoutError, createCheckoutParameters, type CheckoutUrls } from "./model.js";

export interface CheckoutAttempt {
  id: string; user_id: string; license_id: string; tier: string; billing_interval: string;
  status: "creating" | "open" | "completed" | "expired" | "failed";
  stripe_checkout_session_id: string | null; checkout_url: string;
  stripe_parameters: Stripe.Checkout.SessionCreateParams;
  expires_at: Date; created_at: Date;
}
export function createCheckoutRepository(pool: Pool) {
  return {
    async begin(intent: CheckoutIntent, catalog: PriceCatalog, urls: CheckoutUrls): Promise<CheckoutAttempt | { legacyUrl: string }> {
      return withTransaction(pool, async (tx) => {
        const imported = await tx.query<{ complete: boolean }>("SELECT count(*)=3 AS complete FROM billing.cutover_checkpoints WHERE name IN ('legacy_purchases_imported','legacy_subscriptions_imported','legacy_checkouts_imported')");
        if (!imported.rows[0]?.complete) throw new CheckoutError("checkout_recovery_required");
        await tx.query("INSERT INTO billing.accounts(user_id,license_id) VALUES($1,$2) ON CONFLICT(user_id) DO NOTHING", [intent.userId, intent.licenseId]);
        const account = await lockBillingAccount(tx, intent.userId);
        await assertBillingAccountOpen(tx, intent.userId);
        if (account.license_id !== intent.licenseId) throw new CheckoutError("checkout_identity_mismatch");
        const paid = await tx.query("SELECT 1 FROM billing.subscriptions WHERE user_id=$1 AND status IN ('trialing','active') LIMIT 1", [intent.userId]);
        if (paid.rowCount) throw new CheckoutError("subscription_exists");
        const legacy = await tx.query<{ state: string; status: string | null; license_id: string; tier: string; billing_interval: string; checkout_url: string; session_expires_at: Date | null }>(`
          SELECT state,status,license_id,tier,billing_interval,checkout_url,session_expires_at FROM billing.legacy_checkout_recovery
          WHERE user_id=$1 AND (state<>'verified' OR status='open' OR (status='completed'
            AND NOT EXISTS(SELECT 1 FROM billing.subscriptions WHERE user_id=$1)))
          ORDER BY CASE state WHEN 'verified' THEN 1 ELSE 0 END,source_created_at DESC,id LIMIT 1 FOR UPDATE`, [intent.userId]);
        if (legacy.rows[0]) {
          const prior = legacy.rows[0];
          if (prior.license_id !== intent.licenseId) throw new CheckoutError("checkout_identity_mismatch");
          if (prior.state !== "verified" || prior.status !== "open" || !prior.checkout_url || !prior.session_expires_at || prior.session_expires_at.getTime() <= Date.now()) {
            throw new CheckoutError("checkout_recovery_required");
          }
          if (prior.tier !== intent.tier || prior.billing_interval !== intent.interval) throw new CheckoutError("checkout_in_progress");
          return { legacyUrl: prior.checkout_url };
        }
        const incomplete = await tx.query(`SELECT 1 FROM billing.checkout_attempts a WHERE a.user_id=$1 AND a.status='completed'
          AND NOT EXISTS(SELECT 1 FROM billing.subscriptions s WHERE s.user_id=a.user_id) LIMIT 1`, [intent.userId]);
        if (incomplete.rowCount) throw new CheckoutError("subscription_exists");
        const existing = await tx.query<CheckoutAttempt>("SELECT * FROM billing.checkout_attempts WHERE user_id=$1 AND status IN ('creating','open') FOR UPDATE", [intent.userId]);
        if (existing.rows[0]) {
          if (existing.rows[0].tier !== intent.tier || existing.rows[0].billing_interval !== intent.interval) throw new CheckoutError("checkout_in_progress");
          return existing.rows[0];
        }
        const id = randomUUID();
        const expiresAt = new Date(Date.now() + 35 * 60 * 1000);
        const history = await tx.query<{ disqualified: boolean }>(`SELECT
          EXISTS(SELECT 1 FROM billing.subscriptions WHERE user_id=$1) OR
          EXISTS(SELECT 1 FROM billing.legacy_purchases WHERE user_id=$1 AND status='completed') AS disqualified`, [intent.userId]);
        const verifiedIntent = { ...intent, trialEligible: intent.trialEligible && !history.rows[0]!.disqualified };
        const parameters = createCheckoutParameters({ intent: verifiedIntent, attemptId: id, customerId: account.stripe_customer_id, catalog, urls, expiresAt });
        const inserted = await tx.query<CheckoutAttempt>(`INSERT INTO billing.checkout_attempts
          (id,user_id,license_id,tier,billing_interval,status,stripe_parameters,expires_at)
          VALUES($1,$2,$3,$4,$5,'creating',$6::jsonb,$7) RETURNING *`,
          [id, intent.userId, intent.licenseId, intent.tier, intent.interval, JSON.stringify(parameters), expiresAt]);
        return inserted.rows[0]!;
      });
    },

    async prepareReplacement(attempt: CheckoutAttempt, catalog: PriceCatalog, gateway: {
      retrieve: (id: string) => Promise<unknown>;
      cancel: (id: string) => Promise<unknown>;
    }): Promise<void> {
      const blocked = await withTransaction(pool, async (tx) => {
        const account = await lockBillingAccount(tx, attempt.user_id);
        await assertBillingAccountOpen(tx, account.user_id);
        const subscriptions = await tx.query<{ stripe_subscription_id: string; status: string }>(
          "SELECT stripe_subscription_id,status FROM billing.subscriptions WHERE user_id=$1 AND status IN ('trialing','active','past_due')", [account.user_id]);
        let blocked = false;
        for (const subscription of subscriptions.rows) {
          if (["trialing", "active"].includes(subscription.status)) { blocked = true; continue; }
          let canonical = await gateway.retrieve(subscription.stripe_subscription_id);
          const current = normalizeSubscription(canonical, catalog);
          if (current.userId !== account.user_id || current.licenseId !== account.license_id ||
              current.projection.subscriptionId !== subscription.stripe_subscription_id ||
              (account.stripe_customer_id && current.customerId !== account.stripe_customer_id)) throw new CheckoutError("checkout_identity_mismatch");
          if (current.projection.status === "past_due") {
            canonical = await gateway.cancel(subscription.stripe_subscription_id);
            if (normalizeSubscription(canonical, catalog).projection.status !== "canceled") throw new CheckoutError("checkout_unavailable");
          }
          await synchronizeSubscription(tx, { account, subscriptionId: subscription.stripe_subscription_id, catalog, fetchSubscription: async () => canonical });
          if (["trialing", "active"].includes(normalizeSubscription(canonical, catalog).projection.status)) blocked = true;
        }
        // An earlier creation call may have succeeded without its response.
        // Keep its slot until canonical session recovery proves it is finished.
        return blocked;
      });
      if (blocked) throw new CheckoutError("subscription_exists");
    },

    async withOpenAttempt<T>(attempt: CheckoutAttempt, operation: (current: CheckoutAttempt, actions: {
      resolveStatus: (status: "completed" | "expired", sessionId: string) => Promise<void>;
      open: (session: { id: string; url: string; expiresAt: Date }) => Promise<void>;
    }) => Promise<T>): Promise<T> {
      return withTransaction(pool, async tx => {
        await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
        const account = await lockBillingAccount(tx, attempt.user_id);
        await assertBillingAccountOpen(tx, account.user_id);
        const current = (await tx.query<CheckoutAttempt>("SELECT * FROM billing.checkout_attempts WHERE id=$1 AND user_id=$2 FOR UPDATE", [attempt.id, attempt.user_id])).rows[0];
        if (!current || current.license_id !== account.license_id) throw new CheckoutError("checkout_identity_mismatch");
        return operation(current, {
          resolveStatus: async (status, sessionId) => {
            await tx.query(`UPDATE billing.checkout_attempts SET status=$2,stripe_checkout_session_id=$3,updated_at=now() WHERE id=$1
              AND (status IN ('creating','open') OR (status='expired' AND $2='completed'))`, [current.id, status, sessionId]);
          },
          open: async session => {
            const result = await tx.query(`UPDATE billing.checkout_attempts SET status='open',stripe_checkout_session_id=$2,
              checkout_url=$3,expires_at=$4,updated_at=now() WHERE id=$1 AND status IN ('creating','open')`, [current.id, session.id, session.url, session.expiresAt]);
            if (!result.rowCount) throw new CheckoutError("checkout_in_progress");
          },
        });
      });
    },
  };
}
