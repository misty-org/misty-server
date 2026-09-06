import type { PoolClient } from "pg";
import { CheckoutError } from "./model.js";

/** Caller holds the billing-account write lock through the admitted operation. */
export async function assertBillingAccountOpen(tx: PoolClient, userId: string) {
  if ((await tx.query("SELECT 1 FROM billing.account_closures WHERE user_id=$1", [userId])).rowCount) throw new CheckoutError("billing_account_closed");
}
