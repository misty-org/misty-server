# Private billing account closure

Status: durable closure, canonical resource cleanup, billing-admission barriers
and inactive entitlement handling are implemented. Payments active mode mounts
`POST /internal/billing/account-closure` together with its cleanup worker; paused
and delivery modes do neither. The API payments-step client and fenced
acknowledgement have tested factories. Public deletion and its API workers remain
unmounted until provider/local/purge handlers and ownership handover are complete.
A `closing` receipt is never evidence that billing has finished closing.

## Durable request and service boundary

Billing migration 9 introduces `billing.account_closures`. Its account-bound record
is also a permanent admission tombstone. The private command carries version,
user ID, license ID and the original deletion-request ID. It requires an API
EdDSA assertion with the `billing:account-closure` scope, bound to the method,
path, exact body and affected account. It accepts no browser session, App token,
customer ID or Stripe parameters. HTTP input is bounded to 4 KiB and four
concurrent operations. No public SDK methods or billing contracts were added.

The transaction locks the billing account, validates its license, then records
both the closure and a null subscription entitlement in the durable outbox.
Identical requests return the existing state without issuing another entitlement.
Conflicting license/deletion identities or cross-account reuse of a deletion ID
fail without partial rows. The account is frozen even if historical imports have
not finished; remote cleanup must wait for their evidence, rather than postponing
admission revocation. SQL lock/statement deadlines are two/five seconds.

A `closed` state is set only by the cleanup handler after verified remote effects
and no unresolved checkout intent. Closing records survive anonymizing and process
restarts. The forward-only migration does not remove them on application rollback.
The payments runtime needs SELECT, INSERT and UPDATE on the closure table;
existing checkout, summary and entitlement producers need its SELECT privilege,
and entitlement producers also need UPDATE to reopen cleanup after late billing
activity. API roles continue to have no billing-schema access. Runtime DELETE or
TRUNCATE is unnecessary and must not be granted for this tombstone table.

## Checkout, portal and delayed-event behavior

Checkout begin, replacement preparation and final session issuance check the
closure tombstone under the billing-account write lock. Final issuance reloads
the durable attempt and holds the lock through the Stripe call and local result
checkpoint. It can return a cached open session or observe an already-completed
attempt without issuing another create. Terminal responses persist their session
ID and state before the service returns a conflict or retries with a new attempt.
An ambiguous response retains the original creating intent and parameters for
canonical recovery. Closure prevents any later blind create retry.

Portal creation also retains the billing-account lock through its bounded Stripe
call. A closure waits for such an admitted operation; later portal calls fail.
Summary responses no longer advertise a usable portal for a closing account.
These locks serialize normal operations but cannot undo a Stripe operation after
connection/process loss. Cleanup must still account for unresolved external
outcomes and previously issued URLs.

Every subscription entitlement producer suppresses paid projections when the
account has a closure tombstone. It retains revision/outbox evidence with a null
subscription. If a canonical nonterminal subscription appears after closure was
completed, the same tombstone returns to `closing` with a safe error code so the
cleanup worker can handle it. Billing admission remains blocked throughout.
The active worker continues watching retained closure records even after the API
user is anonymized. Late activity immediately makes the closure due again.

The API receiver now accepts ordered signed events for pending/deleted accounts
with the exact retained user/license identity. It persists inbox/projection
history and acknowledges duplicates, but never applies subscription access,
trial eligibility or wallet refresh to an inactive account. Its access deadline
is null. Attributed purchase reversals still revoke the corresponding lifetime
grant and update financial attribution, without refreshing the inactive account's
wallet. Missing attribution remains retryable; conflicting identity or payloads
still fail.

## Canonical cleanup and recovery

Billing migration 10 adds durable per-resource progress and account retry timing.
Resources have globally unique kind/ID ownership, account-bound customer evidence,
pending/completed state and persisted customer pagination. The worker holds the
billing account and closure locks through one resource or one bounded page and its
checkpoint. Concurrent workers skip the locked account. SQL deadlines are two/five
seconds; the worker checks a 45-second operation deadline, with production Stripe
calls limited to eight seconds and no automatic network retries. Failures retain
the closure and back off from 15 seconds to one hour with owned error codes.

All three historical-import checkpoints are required before provider effects.
Sources include native/legacy sessions, completed historical purchases, local
subscriptions and every associated customer, including older one-off customers
different from the account's current customer. Cross-account or conflicting
customer attribution blocks before mutation. Canonical sessions must match their
source's account/license, mode, tier/interval and native attempt identity where
applicable. Open sessions are expired; ambiguous expiration is recovered by a
canonical read because already-expired sessions cannot be expired again.
See [Stripe session expiration](https://docs.stripe.com/api/checkout/sessions/expire).

Subscriptions require validated canonical identity and configured price data.
Nonterminal subscriptions are canceled with `invoice_now:false, prorate:false`,
then the canonical terminal result is persisted. This does not refund completed
payments or guarantee reversal of already pending invoice items or asynchronous
settlements. See [Stripe subscription cancellation](https://docs.stripe.com/api/subscriptions/cancel).

Customer cleanup lists sessions and all subscription statuses in pages of 100,
persisting the cursor between calls with a 10,000-page limit. Newly discovered
sessions/subscriptions are processed before customer deletion. Including canceled
subscriptions keeps the collection stable while cancellations proceed; see
[Stripe subscription listing](https://docs.stripe.com/api/subscriptions/list) and
[session listing](https://docs.stripe.com/api/checkout/sessions/list).
The customer is deleted only after discovered resources and ambiguous checkout
intents are resolved. Completion requires the exact customer ID and `deleted:true`;
a later canonical deleted-customer read recovers a lost acknowledgement. Stripe
retains historical customer records after deletion while removing payment cards
and preventing further customer operations; see
[Stripe customer deletion](https://docs.stripe.com/api/customers/delete).

Unknown checkout creates remain with the existing native/legacy recovery workers;
closure never replays a blind create. Enabled checkout recovery links, unsupported
historical prices and ambiguous ownership remain blocked for operator resolution.
The final transaction clears native checkout URLs and exact request parameters,
retains resource/financial evidence, enqueues a null entitlement and marks closed.
Connection failure during a remote operation discards the PostgreSQL connection;
no commit is accepted after lock loss. The next attempt reads canonical provider
state rather than assuming the remote effect rolled back.

## API acknowledgement

The API worker claims the payments step with the original deletion ID and its
two-minute lease. Its private client signs the exact request, permits no redirect
or automatic retry, bounds calls to ten seconds, allows two concurrent calls and
limits streamed receipts to 4 KiB. It checks all returned account/license/deletion
identities. API SQL locks are released before service I/O.

A valid `closing` receipt defers polling for 30 seconds without manufacturing a
failure or clearing another stage's diagnostic. A verified `closed` receipt can
complete only the payments step. Acknowledgement locks account, request and step,
rechecks pending-deletion lifecycle, license, native ownership and the unexpired
lease token, and commits `billing_closed` evidence atomically. Expired/replaced
workers cannot advance or reset the step. Ambiguous responses use the same durable
request on retry; no public SDK billing functionality is involved.

## Remaining release work and evidence

Finish provider/local/purge handlers and wire the complete API workflow before
mounting public deletion. Drain old Go billing and deletion writers before native
activation. Rehearse real test-mode provider effects, retained checkout URLs,
representative imports, outages, process loss, late events, load and rollback.
No live Stripe operation was performed for this migration work.

Ten restricted-role PostgreSQL tests cover durable retry identity, incomplete
history, command signatures/body binding, checkout/portal races with real locks,
ambiguous creation, blocking stale prepared attempts, late paid projection
suppression, portal availability, atomic outbox failure and contention rollback.
Two API integration tests verify inactive subscriptions and financial reversals
without access or wallet renewal. Existing checkout recovery and entitlement tests
remain part of the full integration suite.

Fourteen additional restricted-role worker tests cover native and legacy cleanup,
historical customer discovery, completed-session identity discovery, lost expire/
cancel/delete responses, missing history, unknown creates, ownership/recovery-link
guards, pagination and false deletion acknowledgements, concurrent workers and an
actual PostgreSQL backend termination during a simulated provider effect. Seven
API bridge integration tests cover the real signed route and empty-account cleanup
roundtrip, lost committed receipts, independent stage diagnostics, unlocked network
I/O, expired/replaced leases, identity and lifecycle/owner changes. Three client unit
tests exercise signature binding, hostile responses and bounded streaming.
