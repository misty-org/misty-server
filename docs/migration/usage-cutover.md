# Hosted AI usage migration

The native implementation is in `apps/api/src/modules/usage`. Go still owns live
AI requests. The TypeScript repository is tested but has not replaced the AI
invocation, agent, automation, semantic-query or media-search callers.

## Implemented behavior

- Personal and Space wallet snapshots use the current account/owner license.
  Space access requires current membership. A Space request consumes both the
  initiating member's personal allowance and the Space allowance, never the
  owner's personal balance merely because they own the Space.
- Exact and partial reservations bind their idempotency key to identity, Space,
  meter, requested amount and partial-reservation policy. Historical rows without
  original request metadata retain Go's matching rule until their first native
  retry records it. New requests require an exact match.
- Settlement clips charges to available capacity while protecting other live
  reservations. Wallet changes, reservation completion and the consumption ledger
  share a transaction. A key belonging to another operation rejects the entire
  transaction instead of silently omitting its ledger entry.
- Released reservations receive a new generation when retried. Settlement,
  release, refund and renewal require the returned generation and account. A late
  completion cannot charge a newer generation. Lease renewal preserves the
  creation timestamp and cannot revive an expired lease.
- Consumption is stored independently of a clamped remaining balance. An account
  that spent 800,000 from Pro's 900,000 allowance still has only 100,000 after a
  Basic-to-Pro cycle. Space wallets have the same protection. Weekly reset is the
  next UTC Monday and retains live reservations.
- Refunds are deduplicated by reservation as well as operation key. Each new
  consumption records the personal/Space wallet period; refunding an old period
  never credits unrelated current-week usage. Personal and Space refund deltas
  are recorded independently, including when plan changes affect them differently.

## Transactions and locking

Native usage operations lock the operation key, then the Space when present,
then all affected account rows in sorted order, then wallets and the reservation.
Space cleanup includes the accounts owning its stale reservations in that sorted
set. Entitlement effects lock the account before license/wallet changes. Native ownership
transfer locks the Space before the affected account rows.

The PostgreSQL suite covers concurrent personal reservations, shared Space
capacity, protection of another member's reservation, overlapping Space cleanup,
generation fencing, lease renewal, both plan-cycle cases, duplicate refunds,
period boundaries, authorization and rollback after a ledger write failure.
These are local correctness checks, not the final production load test.

## Remaining cutover gates

1. The native billing usage endpoint is implemented; see `billing-usage-cutover.md`.
   Port and wire every hosted-AI caller and rate-card estimator.
   Keep `assistant_ai` as the persisted meter identifier. Long-running calls must
   retain their generation and renew while active; terminal paths must settle,
   release or refund exactly the reservation they own.
2. Stop Go usage writers and drain or explicitly hand over in-flight reservations
   before switching ownership. Go does not maintain the new consumption counters,
   generations or leases. Do not run both implementations as wallet writers.
3. Verify historical refund period attribution before processing an old unsettled
   refund. Old ledger rows do not record their wallet periods; the native refund
   path rejects missing period evidence instead of guessing. Already-refunded
   reservations remain idempotent. Add a reviewed attribution/import procedure.
4. The migration preserves the current balance and derives initial consumption
   from it. It cannot reconstruct consumption already lost by old plan clamping,
   particularly where historical personal and Space refund amounts differed.
   Validate representative production snapshots and document any exceptions.
5. Rehearse deletion, membership removal, owner transfer, provider cancellation,
   process crashes, period rollover and recovery together with real invocations.
   Verify load/lock behavior for large Spaces with many stale reservations; the
   current cleanup transaction includes all stale accounts in that Space.
6. Provision restricted runtime roles, add usage/lease/queue observability and
   complete the ownership-switch and rollback rehearsal before retiring Go.
