import type { PoolClient } from "pg";
import { ClosureCleanupError, customerSchema, resourcePageSchema, type ClosureAccount, type ClosureGateway, type ClosureResource } from "./model.js";
import { assertClosureCustomer, completeClosureResource, enqueueClosureResource, unresolvedClosureCheckouts } from "./resources.js";

export async function closeCustomer(tx: PoolClient, account: ClosureAccount, resource: ClosureResource, gateway: ClosureGateway, signal: AbortSignal) {
  await assertClosureCustomer(tx, account.user_id, resource.resource_id);
  const customer = customerSchema.parse(await gateway.retrieveCustomer(resource.resource_id));
  if (customer.id !== resource.resource_id || (customer.metadata?.user_id && customer.metadata.user_id !== account.user_id) ||
    (customer.metadata?.license_id && customer.metadata.license_id !== account.license_id)) throw new ClosureCleanupError("closure_identity_mismatch");
  signal.throwIfAborted();
  // Stripe retains a retrievable deletion tombstone. It is also the recovery
  // evidence when a prior delete succeeded but its response/SQL commit was lost.
  if (customer.deleted) { await completeClosureResource(tx, account.user_id, "customer", resource.resource_id); return; }
  if (resource.phase === "delete") {
    if (await unresolvedClosureCheckouts(tx, account.user_id)) throw new ClosureCleanupError("closure_recovery_required");
    const deleted = customerSchema.parse(await gateway.deleteCustomer(resource.resource_id));
    if (deleted.id !== resource.resource_id || !deleted.deleted) throw new ClosureCleanupError("closure_provider_unavailable");
    signal.throwIfAborted(); await completeClosureResource(tx, account.user_id, "customer", resource.resource_id); return;
  }
  if (resource.pages >= 10000) throw new ClosureCleanupError("closure_scan_limit");
  const query = { customer: resource.resource_id, limit: 100 as const, ...(resource.cursor ? { starting_after: resource.cursor } : {}) };
  const page = resourcePageSchema.parse(await (resource.phase === "sessions" ? gateway.listSessions(query) : gateway.listSubscriptions({ ...query, status: "all" })));
  const kind = resource.phase === "sessions" ? "checkout" : "subscription";
  for (const entry of page.data) {
    if (entry.customer !== resource.resource_id || !entry.id.startsWith(kind === "checkout" ? "cs_" : "sub_")) throw new ClosureCleanupError("closure_identity_mismatch");
    signal.throwIfAborted(); await enqueueClosureResource(tx, account.user_id, kind, entry.id, resource.resource_id);
  }
  const cursor = page.data.at(-1)?.id;
  if (page.has_more && (!cursor || cursor === resource.cursor || page.data.some(entry => entry.id === resource.cursor))) throw new ClosureCleanupError("closure_pagination_invalid");
  if (page.has_more) await tx.query("UPDATE billing.account_closure_resources SET cursor=$3,pages=pages+1,updated_at=now() WHERE kind='customer' AND user_id=$1 AND resource_id=$2", [account.user_id, resource.resource_id, cursor]);
  else await tx.query("UPDATE billing.account_closure_resources SET phase=$3,cursor=NULL,pages=pages+1,updated_at=now() WHERE kind='customer' AND user_id=$1 AND resource_id=$2", [account.user_id, resource.resource_id, resource.phase === "sessions" ? "subscriptions" : "delete"]);
}
