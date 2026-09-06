import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { assertBillingAccountOpen } from "./admission.js";
import { CheckoutError } from "./model.js";

export function createPortalService(options: {
  pool: Pool;
  returnUrl: string;
  createSession: (customerId: string, returnUrl: string, idempotencyKey: string) => Promise<{ url: string }>;
}) {
  return {
    async create(userId: string, licenseId: string, requestId: string): Promise<string> {
      return withTransaction(options.pool, async tx => {
        await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
        const result = await tx.query<{ stripe_customer_id: string | null }>(
          "SELECT stripe_customer_id FROM billing.accounts WHERE user_id=$1 AND license_id=$2 FOR UPDATE", [userId, licenseId]);
        const customerId = result.rows[0]?.stripe_customer_id;
        if (!customerId) throw new CheckoutError("portal_unavailable");
        await assertBillingAccountOpen(tx, userId);
        const session = await options.createSession(customerId, options.returnUrl, `misty-portal-${requestId}`);
        const url = new URL(session.url);
        if (url.protocol !== "https:" || url.username || url.password) throw new CheckoutError("checkout_unavailable");
        return session.url;
      });
    },
  };
}
