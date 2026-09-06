import type { PoolClient } from "pg";
import { normalizeSubscription, type PriceCatalog } from "../subscriptions/model.js";
import { synchronizeSubscription } from "../subscriptions/repository.js";
import { ClosureCleanupError, type ClosureAccount, type ClosureGateway, type ClosureResource } from "./model.js";
import { assertClosureCustomer, completeClosureResource, enqueueClosureResource } from "./resources.js";

export async function closeSubscription(tx: PoolClient, account: ClosureAccount, resource: ClosureResource, gateway: ClosureGateway, catalog: PriceCatalog) {
  let raw = await gateway.retrieveSubscription(resource.resource_id);
  const verify = (value: unknown) => {
    const current = normalizeSubscription(value, catalog);
    if (current.userId !== account.user_id || current.licenseId !== account.license_id || current.projection.subscriptionId !== resource.resource_id ||
      (resource.customer_id && current.customerId !== resource.customer_id)) throw new ClosureCleanupError("closure_identity_mismatch");
    return current;
  };
  let canonical = verify(raw); await assertClosureCustomer(tx, account.user_id, canonical.customerId);
  if (!["canceled", "incomplete_expired"].includes(canonical.projection.status)) {
    raw = await gateway.cancelSubscription(resource.resource_id); canonical = verify(raw);
    if (canonical.projection.status !== "canceled") throw new ClosureCleanupError("closure_provider_unavailable");
  }
  // Historical purchases may refer to an older customer than the account's
  // current one. The verified resource customer binds this canonical operation.
  await synchronizeSubscription(tx, { account: { ...account, stripe_customer_id: canonical.customerId }, subscriptionId: resource.resource_id, catalog, fetchSubscription: async () => raw });
  await enqueueClosureResource(tx, account.user_id, "customer", canonical.customerId, canonical.customerId);
  await completeClosureResource(tx, account.user_id, "subscription", resource.resource_id);
}
