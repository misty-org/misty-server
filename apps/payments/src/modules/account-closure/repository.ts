import type { Pool } from "pg";
import { withTransaction } from "../../../../../packages/database/src/transaction.js";
import { billingClosureResultSchema, type BillingClosureIntent } from "../../../../../packages/service-contracts/src/payments.js";
import { lockBillingAccount } from "../subscriptions/repository.js";
import { enqueueEntitlement } from "../entitlements/repository.js";

export class BillingClosureConflict extends Error {}
export class BillingClosureUnavailable extends Error {}
export function createBillingClosureRepository(pool: Pool) {
  return {
    async begin(input: BillingClosureIntent) {
      try {
        return await withTransaction(pool, async tx => {
          await tx.query("SET LOCAL lock_timeout='2s'"); await tx.query("SET LOCAL statement_timeout='5s'");
          await tx.query("INSERT INTO billing.accounts(user_id,license_id) VALUES($1,$2) ON CONFLICT(user_id) DO NOTHING", [input.userId, input.licenseId]);
          const account = await lockBillingAccount(tx, input.userId);
          if (account.license_id !== input.licenseId) throw new BillingClosureConflict();
          const existing = (await tx.query<{ deletion_request_id: string; license_id: string; state: string }>(
            "SELECT deletion_request_id,license_id,state FROM billing.account_closures WHERE user_id=$1 FOR UPDATE", [input.userId])).rows[0];
          if (existing) {
            if (existing.deletion_request_id !== input.deletionRequestId || existing.license_id !== input.licenseId) throw new BillingClosureConflict();
            return billingClosureResultSchema.parse({ ...input, state: existing.state });
          }
          // Freeze even when a historical import is incomplete. The cleanup
          // handler must wait for its evidence; freezing must not wait for it.
          await tx.query("INSERT INTO billing.account_closures(user_id,license_id,deletion_request_id) VALUES($1,$2,$3)", [input.userId, input.licenseId, input.deletionRequestId]);
          await enqueueEntitlement(tx, { userId: input.userId, licenseId: input.licenseId, subscription: null });
          return billingClosureResultSchema.parse({ ...input, state: "closing" });
        });
      } catch (error) {
        const pg = error as { code?: string; constraint?: string };
        if (pg?.code === "23505" && pg.constraint === "account_closures_deletion_request_id_key") throw new BillingClosureConflict();
        if (["55P03", "57014", "40P01"].includes(pg?.code ?? "")) throw new BillingClosureUnavailable();
        throw error;
      }
    },
  };
}
