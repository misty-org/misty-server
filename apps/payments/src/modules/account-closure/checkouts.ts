import type { PoolClient } from "pg";
import { ClosureCleanupError, closureSessionSchema, type ClosureAccount, type ClosureGateway, type ClosureResource, type ClosureSession } from "./model.js";
import { assertClosureCustomer, completeClosureResource, enqueueClosureResource } from "./resources.js";

async function verify(tx: PoolClient, account: ClosureAccount, resource: ClosureResource, session: ClosureSession) {
  if (session.id !== resource.resource_id || (resource.customer_id && session.customer !== resource.customer_id)) throw new ClosureCleanupError("closure_identity_mismatch");
  const native = (await tx.query<{ id: string; license_id: string; tier: string; billing_interval: string; expected_customer: string | null }>("SELECT id,license_id,tier,billing_interval,stripe_parameters->>'customer' AS expected_customer FROM billing.checkout_attempts WHERE user_id=$1 AND stripe_checkout_session_id=$2", [account.user_id, session.id])).rows[0];
  const legacy = (await tx.query<{ license_id: string; tier: string; billing_interval: string }>("SELECT license_id,tier,billing_interval FROM billing.legacy_checkout_recovery WHERE user_id=$1 AND (stripe_checkout_session_id=$2 OR source_session_id=$2)", [account.user_id, session.id])).rows[0];
  const purchase = (await tx.query<{ license_id: string; stripe_payment_intent_id: string | null; stripe_customer_id: string | null }>("SELECT license_id,stripe_payment_intent_id,stripe_customer_id FROM billing.legacy_purchases WHERE user_id=$1 AND stripe_checkout_session_id=$2", [account.user_id, session.id])).rows[0];
  if (native && legacy || (purchase && (native || legacy))) throw new ClosureCleanupError("closure_identity_mismatch");
  if (purchase) {
    if (purchase.license_id !== account.license_id || session.mode !== "payment" || session.status !== "complete" ||
      (purchase.stripe_customer_id && purchase.stripe_customer_id !== session.customer) ||
      (purchase.stripe_payment_intent_id && purchase.stripe_payment_intent_id !== session.payment_intent)) throw new ClosureCleanupError("closure_identity_mismatch");
  } else {
    if (session.client_reference_id !== account.user_id || session.metadata?.user_id !== account.user_id || session.metadata.license_id !== account.license_id) throw new ClosureCleanupError("closure_identity_mismatch");
    if ((native?.expected_customer && native.expected_customer !== session.customer) || (legacy && account.stripe_customer_id && account.stripe_customer_id !== session.customer)) throw new ClosureCleanupError("closure_identity_mismatch");
    const attempt = native ?? legacy;
    if (attempt && (attempt.license_id !== account.license_id || session.mode !== "subscription" || session.metadata.kind !== "subscription" ||
      session.metadata.tier !== attempt.tier || session.metadata.interval !== attempt.billing_interval ||
      (native ? session.metadata.checkout_attempt_id !== native.id : Boolean(session.metadata.checkout_attempt_id)))) throw new ClosureCleanupError("closure_identity_mismatch");
  }
  if (session.customer) await assertClosureCustomer(tx, account.user_id, session.customer);
  if (session.after_expiration?.recovery?.enabled) throw new ClosureCleanupError("closure_recovery_link_enabled");
  if (session.mode === "subscription" && session.status === "complete" && (!session.subscription || !session.customer)) throw new ClosureCleanupError("closure_recovery_required");
}
export async function closeCheckout(tx: PoolClient, account: ClosureAccount, resource: ClosureResource, gateway: ClosureGateway) {
  let session = closureSessionSchema.parse(await gateway.retrieveSession(resource.resource_id));
  await verify(tx, account, resource, session);
  if (session.status === "open") {
    session = closureSessionSchema.parse(await gateway.expireSession(session.id));
    await verify(tx, account, resource, session);
    if (session.status !== "expired") throw new ClosureCleanupError("closure_provider_unavailable");
  }
  if (session.customer) await enqueueClosureResource(tx, account.user_id, "customer", session.customer, session.customer);
  if (session.subscription) await enqueueClosureResource(tx, account.user_id, "subscription", session.subscription, session.customer);
  const status = session.status === "complete" ? "completed" : "expired";
  await tx.query(`UPDATE billing.checkout_attempts SET status=$3,checkout_url='',updated_at=now() WHERE user_id=$1 AND stripe_checkout_session_id=$2`, [account.user_id, session.id, status]);
  await tx.query(`UPDATE billing.legacy_checkout_recovery SET state='verified',status=$3,stripe_checkout_session_id=$2,checkout_url='',session_expires_at=$4,
    verified_at=now(),recovery_cursor=NULL,candidate_session_id=NULL,last_recovery_error=NULL WHERE user_id=$1 AND (stripe_checkout_session_id=$2 OR source_session_id=$2)`, [account.user_id, session.id, status, new Date(session.expires_at * 1000)]);
  await completeClosureResource(tx, account.user_id, "checkout", resource.resource_id);
}
