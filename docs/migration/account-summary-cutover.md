# Native account summary and private billing history

`GET /me`, `/api/me` and `/v1/me` now have a native implementation. They preserve
identity/profile/avatar fields, license tier/status/device, trial history and the
existing nested billing summary. Only full account sessions are accepted. No new
public SDK method or billing contract is introduced.

## Service ownership and behavior

The API reads its own user, session and license tables. Hosted summaries obtain
payment metadata through `POST /internal/billing/summary` on the independent
payments service. That service reads only its billing schema; it makes no Stripe
network request for a summary. Neither service receives the other's database
credentials. Private definitions live in `packages/service-contracts`, outside
the open-source SDK snapshot.

The API signs the exact method, path and body using its Ed25519 service key, with
subject equal to the account and scope `billing:summary`. Payments verifies the
issuer/audience, key, lifetime, action, body and subject. Account cookies, App
tokens, injected customer IDs and assertions for checkout cannot authorize this
route. Responses bind user/license identity and contain only the subscription
summary and completed-purchase flag needed by the API; raw Stripe identifiers
are not added to `/me`.

The client uses a configured HTTPS endpoint, no redirects or automatic retries,
a five-second deadline, sixteen concurrent requests and an 8192-byte response
limit. Streaming responses retain admission until completion and support explicit
cancellation. Errors are sanitized into `account_summary_unavailable` (503).
The API rechecks the exact unexpired session and active account after the network
call. SQL transactions and row locks are not held across that call.

The API remains authoritative for license access. An active subscription in a
summary does not grant an API tier before entitlement delivery. Billing-kind
selection preserves Go's trial, paid subscription, lifetime, then free preference.
Any historical subscription, including a canceled one, prevents trial eligibility;
the separate completed-purchase check retains Go's current-status rule. Local
trial history and lifetime grants also prevent eligibility. The account response
is a read, not authorization to create checkout; checkout must independently
serialize and revalidate the eligibility decision with trial-grant paths.

Self-host summaries use local license state without contacting payments and
check the current local entitlement and administrative disablement. Self-host
startup rejects private payment client configuration.

## History import

Missing history must not masquerade as a new free account. Payments returns 503
until both operator-owned `legacy_purchases_imported` and
`legacy_subscriptions_imported` checkpoints exist. Runtime roles can read those
markers but cannot create them. Fresh databases with no historical records still
run the verified empty imports before enabling hosted summaries.

The purchase import remains `npm run import:legacy-purchases`. After it passes,
`npm run import:legacy-subscriptions` provides a read-only report by default.
Only `-- --commit` copies data. It uses `BILLING_DB_MIGRATION_USER` and
`BILLING_DB_MIGRATION_PASSWORD` on the shared migration database and requires all
Go/native billing writers to be stopped through the ownership switch. It obtains
bounded table locks and the common import advisory lock. These commands are
operator migration tools, never API/runtime endpoints.

The subscription import verifies that purchase history still exactly matches its
verified copy, validates subscription/account/license relationships and supported
states, and rejects conflicting customer ownership or existing target records.
It retains subscription status, price, interval, period/cancellation, reconciliation
state and timestamps. The native event timestamp uses Stripe's integer seconds;
non-integral source values require review. Migration 005 stores the complete
original Go row, including fields absent from the native table, for exact source
verification. Runtime credentials do not receive writes to this archive.

Customer selection follows Go's effective subscription (paid, past due, then other
states, newest first), falling back to the latest purchase customer. Stable IDs
settle equal-timestamp ties where Go had no specified order. A previously imported
account with no customer can receive that verified selection; a different existing
customer is a conflict. The import never rewrites historical license tiers.
Migration 006 indexes the same subscription order for ordinary summary reads.

Account copies, subscriptions, original-record snapshots and the completion marker
commit together. Repeated imports verify unchanged data rather than overwriting
native progress. Once native billing writers run, this importer is no longer a
general synchronization tool. Changed source snapshots, missing rows or modified
native subscriptions require explicit review. The CLI reports counts and sanitized
errors without printing customer records.

## Configuration and evidence

Hosted API composition accepts `PAYMENTS_SUMMARY_URL` ending exactly in
`/internal/billing/summary`, `API_SIGNING_KEY_FILE` (Ed25519 PKCS#8 PEM) and
`API_SIGNING_KEY_ID`. All three are required together. The payments service's
existing `API_VERIFICATION_KEYS_FILE` must contain the matching public key.
Loopback HTTP is allowed only in development/test. Omitting the client keeps the
hosted summary unavailable instead of synthesizing billing history.

Seven PostgreSQL tests run the real payments HTTP app, its private signing boundary,
restricted billing role and native API account handler together. They cover all
aliases and fields, incomplete imports, role isolation, subscription precedence,
trial/lifetime/purchase rules, forged assertions, identity conflict, logout and
deactivation during the service call, and self-host behavior. Runtime-role audit rejects writes to migration
history, completion checkpoints and original import snapshots, including accidental
overbroad grants. Four client tests
cover endpoint validation, malformed/foreign/oversized responses, single-request
behavior, streaming admission and cancellation. Six import tests exercise dry-run,
exact copies, original snapshots, customer selection, source drift, conflicts,
runtime-role rejection and rollback after a failure late in snapshot persistence.

## Remaining ownership gates

The importer deliberately does not send entitlement events or replay checkout
requests. The separate `initialize:entitlements` command now verifies current
license state and queues initial snapshots atomically. Unchanged canonical
reconciliation also initializes its first snapshot once. Signed-delivery tests
preserve trial history and spent/reserved usage; see
`initial-entitlements-cutover.md` for operator steps and delivery evidence.
Representative-data rehearsal, purchase-to-lifetime attribution and pending account
deletion still need their migration handling.

Legacy unresolved checkout attempts now have a separate verified import, durable
recovery and operator review path; see `legacy-checkout-cutover.md`. Rehearse it
before enabling checkout. Native website-only checkout/portal facades now recheck eligibility under their
respective account locks; see `website-billing-cutover.md`. Provision production roles
and service keys, and rehearse imports, outages, load and rollback on representative
data. The summary snapshot is not a substitute for those gates. No live import,
provider request, deployment or billing ownership switch has been performed.
