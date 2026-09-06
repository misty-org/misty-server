# Initial subscription entitlement handover

Subscription import alone does not establish an API entitlement projection.
The operator command `npm run initialize:entitlements` now verifies the imported
history and reports eligible accounts without mutation. `-- --commit` queues each
eligible account's effective subscription once. It does not directly update an
API license, wallet, inbox or projection. Ordinary signed delivery performs those
changes through the independent API receiver and its transaction.

## Preconditions and ownership

Apply billing migrations through 007, then complete the verified purchase and
subscription imports with separate migration credentials. The command requires
`BILLING_DB_MIGRATION_USER` and `BILLING_DB_MIGRATION_PASSWORD` and access to both
schemas on the migration database. This is an operator tool; runtime services
retain their separate restricted roles and cannot perform its cross-schema reads.

Stop every Go/native billing and license writer before the imports and keep them
stopped through initialization and delivery verification. That includes checkout,
trial grants, webhook consumers, reconciliation, expiry and license administration.
The initializer takes the shared import advisory lock and bounded source/target
table locks. After it queues events, enable only the intended native delivery and
API receiver to drain them before resuming ordinary writers. Use `PAYMENTS_MODE=delivery` with API expiry disabled for that drain.
The command's locks cannot replace this ownership discipline after its transaction commits.

Run the dry report first. It rechecks original subscription snapshots, exact native
copies, purchase history and account/customer identities under one consistent
snapshot. It is deliberately a cutover tool, not a resynchronization command after
native billing starts changing the imported subscriptions.

## Access and delivery guarantees

For active accounts with subscription history and no queued snapshot, compare the
API's current tier, status and expiry with the effective subscription plus recorded
lifetime fallback. The initializer and API receiver use the same private pure
subscription policy. Any discrepancy increments `conflicts`; commit rejects the
entire batch if this count is nonzero. Review the affected license/source records
and their provenance; do not automatically normalize a manual grant or local trial
to make a report pass. The command does not change historical license grants.

Accounts without subscription history receive no synthetic null snapshot. Their
API-owned local trials therefore remain untouched. Inactive accounts increment
`deferredInactive` and retain their uninitialized marker; their restoration needs
the account-lifecycle handover before release. Date-based access still follows the
existing trial deadline and paid 72-hour fail-safe at actual delivery time.

Migration 007 adds `billing.accounts.subscription_snapshot_enqueued`. It backfills
only accounts with an existing subscription snapshot in the outbox. A historical
purchase-reversal revision alone does not count. Normal snapshot production stores
the event, next revision and marker in the same account-locked transaction. An
unchanged canonical subscription now emits its first snapshot if needed. Repeated
reconciliation and future pruning of delivered outbox rows do not reset the marker.

The initializer reads pages of 100 accounts in stable ID order and commits one
atomic batch. It reports counts as decimal strings and prints no customer records.
`candidates` identifies eligible accounts; `enqueued` counts this commit's writes;
`alreadyEnqueued` identifies prior snapshot production. Production is distinct
from acknowledgement and application:

- `awaitingAcknowledgement`: latest subscription snapshot lacks a delivered outbox state.
- `awaitingProjection`: the API lacks that revision or a newer one for the same account/license.
- `missingDeliveryEvidence`: a marked account's snapshot is no longer in the outbox; retained marker alone cannot prove delivery.

Before declaring the initial delivery drained, require no new candidates,
conflicts, pending acknowledgements/projections or missing evidence, and review
all deferred inactive accounts. Do not prune the outbox during this handover.
A lost acknowledgement can show an applied projection with delivery still pending;
the same event is retried and deduplicated without repeating its license/wallet effects.

## Local evidence and remaining release work

Seven PostgreSQL tests exercise dry-run and import prerequisites, restricted-role
isolation, real signed HTTP delivery with actual API effects, a lost acknowledgement,
trial history, lifetime fallback, local trials, inactive/restored accounts, used
weekly credits, a live reservation, source/license conflicts, transaction rollback,
out-of-order snapshots, concurrent unchanged reconciliation, reversal-only revisions,
marker retention and a 101-account batch. This is local verification; representative
production-data rehearsal, operator review, queue monitoring, account lifecycle and
rollback evidence remain release gates. Legacy checkout recovery is separately implemented; see `legacy-checkout-cutover.md`.
Its representative-data rehearsal must pass before checkout ownership changes. Go remains the deployed authority.
