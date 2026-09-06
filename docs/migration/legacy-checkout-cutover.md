# Legacy checkout handover

The native payments service now preserves and recovers Go subscription checkout
attempts without replaying their creation requests. The public SDK remains free
of billing methods. Website-only checkout/portal facades are now native (see
`website-billing-cutover.md`); representative ownership-switch rehearsal remains; Go remains deployed.

## Import and isolation

Apply billing migrations through 008. While all Go/native billing writers are
stopped, run the verified purchase and subscription imports, then
`npm run import:legacy-checkouts`. It is read-only by default; `-- --commit`
requires separate `BILLING_DB_MIGRATION_USER` and
`BILLING_DB_MIGRATION_PASSWORD` credentials on the migration database.

The import verifies source account/license identity, finite creation windows,
subscription/purchase history and conflicts with existing native checkouts. It
copies every original Go attempt as JSON into operator-owned
`billing.legacy_checkout_records` and creates a separate recovery ledger in
`billing.legacy_checkout_recovery`. IDs remain text, including non-UUID historical
IDs. Original status, session ID and timestamps remain available independently of
recovered state. Original URLs are archived but are never returned as verified URLs.

Account creation, source snapshots, recovery records and the
`legacy_checkouts_imported` checkpoint commit atomically under the common import
advisory lock and bounded source/target table locks. Repeated imports reject source
or identity drift and preserve recovery progress. This is not a general source
synchronizer after native subscription writers start changing imported state.
Runtime roles cannot write the source archive or completion checkpoint; startup
role audit rejects accidental grants. Payments runtime cannot access API tables.

Native checkout creation requires the verified global checkout import, including
an empty import on a new database. Under the billing account lock, unresolved or
reviewed legacy attempts block replacement creation. A recovered open session can
return its verified HTTPS URL for the same plan while it is unexpired. An imported
attempt can never supply fabricated native creation parameters or an old
idempotency key. Active subscriptions continue to prevent duplicate checkout.

## Recovery behavior

The worker exposes only Stripe retrieval/listing and canonical subscription reads.
A known session ID is retrieved directly. Identity, mode, account/license metadata,
plan/interval metadata and customer consistency are validated before updating the
ledger. Complete sessions must identify a subscription; canonical subscription
state, the entitlement outbox and completed recovery commit together. Any reported
subscription is reconciled even if the session status is already expired.

A missing session ID waits until the original creation window has expired. Recovery
requests one page of at most 100 sessions over the fixed interval from original
creation minus 60 seconds through original expiry. This matches the Go request's
explicit expiry; clock skew beyond that allowance requires operator review in the
representative-data rehearsal. New native attempts carry distinct metadata and
cannot be adopted as Go attempts. Apparent historical matches must validate exact
identity and original expiry, and cannot already belong to another attempt.

The worker stores both its cursor and candidate ID. It exhausts the full window
before adopting a candidate and retrieves the candidate again before using its
current status. Distinct matches across pages, overlapping attribution, identity
conflicts, stalled pagination and a 10,000-page search limit require review. A
complete empty window can release a missing creating/failed/expired attempt; a
missing completed/open session stays blocked for review. The original Go writer
must remain stopped so it cannot issue new creation calls behind this fixed scan.

Provider failure or malformed/incomplete provider state retains the attempt and
uses bounded exponential backoff. One account lock serializes recovery with
checkout and other subscription writers. Each run does at most one list page and
the necessary bounded canonical reads; the Stripe client uses an eight-second
request deadline without automatic network retries. No recovery path creates,
cancels, expires or charges a Stripe session.

Stripe documents the [session retrieval API](https://docs.stripe.com/api/checkout/sessions/retrieve)
and [session listing API](https://docs.stripe.com/api/checkout/sessions/list), including
the created interval, 100-record limit and cursor pagination. These contracts do
not make an arbitrary metadata match unique; the full-window and attribution
checks above remain necessary.

## Operating modes and review

`PAYMENTS_MODE` defaults to `paused`: webhook ingestion and signed billing summaries
are available, but provider workers and checkout/portal commands are disabled.
`delivery` enables only the existing entitlement dispatcher, allowing initial
snapshots to drain without starting reconciliation or checkout recovery. `active`
enables all six payments workers and the private checkout/portal/closure commands. Keep
website traffic disabled until ownership and release gates pass. Configure
`MISTY_NATIVE_ENTITLEMENT_EXPIRY_JOBS=1` on the hosted API only when it owns expiry;
loading payment verification keys alone no longer starts that job.

`npm run recover:legacy-checkouts` prints grouped state/error counts and up to 100
review attempt IDs, without customer IDs, checkout URLs or original records.
`-- --after <attempt-id>` pages through review items using the returned cursor.
After investigating and correcting the underlying evidence, use
`-- --retry <attempt-id>` to requeue a review item under its account lock. This
resets the scan and cannot force a session choice or bypass identity checks.
Unresolved duplicate sessions or externally altered metadata can require provider
or source correction by an operator; the command never performs that correction.

Before switching checkout ownership, account for every pending/review item. A
verified open session is still live and continues through its original URL;
verified completed sessions must have canonical subscription and delivered API
entitlement evidence. Do not erase the Go source or either import archive during
rehearsal or rollback. After native billing writes start, returning to Go requires
an explicit reverse-state reconciliation and stopping native workers; switching
routing alone is not a verified rollback.

## Local evidence

Thirteen PostgreSQL tests cover exact/dry/repeated import, rollback after late
persistence failure, source drift, runtime-role isolation, native checkout
conflicts, URL verification, canonical completion, persisted pagination across
worker restarts, cross-page ambiguity, provider backoff, subscription/outbox
rollback, absent and missing completed sessions, identity/window errors, review
report/requeue and concurrent workers. Existing native checkout regressions remain
passing. Three worker tests verify paused/delivery/active job ownership and drain.
Container smoke checks all three payments modes and verifies disabled command
routes, in addition to hosted/self-host API isolation and graceful shutdown.
Representative production history, actual Stripe test-mode recovery, provider
metadata mutation, ownership switch and reverse-state rollback still require
release rehearsal. Local fixtures do not establish those results.
