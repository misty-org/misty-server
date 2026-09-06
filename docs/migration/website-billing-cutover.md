# Native website billing commands

The Hono API now implements account-only `POST /billing/checkout-session` and
`POST /billing/portal-session`, including `/api` and `/v1` aliases. Successful public
responses remain `{ "url": "..." }`. The public SDK gains no billing methods,
private contracts, Stripe dependencies or service keys.

The existing retired endpoints also have native responses:
`/billing/trial/start` returns 410 with `trial_checkout_required` and the existing
instruction to start the 14-day Pro trial through checkout. It never grants a
local trial. `/billing/credit-checkout-session` returns 410 with `retired_product`
and the existing message that AI agent usage add-ons are no longer sold.

## Ownership, authorization and bounded work

Account bearer sessions and account cookies use the normal API authentication and
origin boundary. App runtime credentials cannot access any of these routes.
The API rechecks the exact unexpired session and active user against its database
before issuing a private command. Self-host mode retains the existing 501 billing
feature gate and rejects configured hosted payment clients.

The checkout input accepts only tier and interval. Account/license identity and
email come from authenticated API records; callers cannot choose customer IDs,
price IDs, return URLs or trial eligibility. Portal request bodies cannot override
the authenticated account's customer or the configured return URL. Response URLs
must use HTTPS without embedded credentials and are never logged by these modules.

Commands have a shared admission limit of two across both actions and all aliases,
a 4096-byte input limit, and bounded per-account sliding windows: ten checkout
requests per minute and twenty portal requests per minute. Retired actions retain
ten requests per minute each. The limiter is process-local with bounded key memory;
replica-wide traffic policy and capacity remain release-verification concerns.

The API takes user, license and exact session locks before calculating local trial
eligibility, and retains those locks through one private command. A two-second SQL
lock deadline bounds contention; the client allows two calls and enforces a
25-second network/response deadline. This deliberately consumes an API connection
while authorizing a remote mutation, unlike the read-only account summary. There
is no synchronous payments-to-API callback in the command transaction. Pool sizing
and contention under realistic provider latency still require load rehearsal.

A second wall-clock session check occurs after acquiring the locks and another
before returning a capability URL. Logout, deletion and local license updates
serialize with authorization. If the response is lost or the caller's session
expires during processing, the remote action may already have committed; the API
does not attempt to undo it or blindly resend it.

## Trial eligibility and retries

The API signs local eligibility only for Pro when neither trial history nor a
recorded lifetime tier exists. Payments requires all three verified historical
imports before checkout and rechecks its own subscription and completed-purchase
history under the billing account lock. Any historical subscription, including
canceled, or a currently completed purchase removes trial eligibility. Max never
receives an automatic Pro trial.

The resulting Stripe request is persisted before creation. A retry of an existing
attempt retains its original parameters and idempotency key, even if newer account
input differs. This preserves an already-authorized offer and handles an ambiguous
provider response without creating another session. New attempts re-evaluate both
owners' state. Active subscriptions and unresolved legacy attempts still block
replacement according to the verified checkout repositories.

The service request is Ed25519-signed for its exact method, action, path, body,
subject, issuer and audience. Private success responses include version and
account/license identity; the API validates them and removes those fields from the
public result. Error codes are allowlisted, success/error bodies are capped at
8192 bytes, redirects and automatic HTTP retries are disabled, and response streams
retain admission until completion or cancellation. Provider/internal diagnostics
are sanitized. Existing validation/conflict messages are retained; bounded service
or database contention failures return a temporary-unavailable 503.

## Configuration and release gates

Set hosted API `PAYMENTS_COMMANDS_URL` to the fixed trusted endpoint ending exactly
in `/internal/billing`, together with `API_SIGNING_KEY_FILE` (Ed25519 PKCS#8 PEM)
and `API_SIGNING_KEY_ID`. The payments service must trust the matching public key.
Production calls require HTTPS; development/test permits explicit loopback HTTP.
The independent summary client uses `PAYMENTS_SUMMARY_URL` and the same API signing
keys. Either client can be configured independently; an omitted client leaves its
hosted endpoint unavailable. Self-host startup rejects these private clients.

Payments must be in `PAYMENTS_MODE=active` to mount its checkout and portal commands.
That setting is not permission to switch live website traffic. Complete the import,
initial entitlement, legacy recovery and ownership procedures first. Go remains
deployed, and both services continue to report migration-incomplete readiness.

Eleven PostgreSQL integration cases connect the real Hono API to a real local
payments HTTP listener using separate restricted roles and signed requests. They
cover aliases, retired endpoints, App denial, cookies/origin protection, history
completeness, portal identity, each trial exclusion, a lost provider response,
license/session lock contention, revoked sessions, wall-clock expiry, concurrent
admission, shared rate limits and self-host denial. Three client tests cover
configuration, malformed/foreign/oversized responses, error sanitization, single
send behavior and streaming cancellation. Image smoke loads hosted billing client
configuration and checks account-only/disabled-self-host routes, while verifying
Stripe remains absent from the API image.

`GET /billing/usage` now composes native storage, personal/Space wallets and the
private subscription summary; see `billing-usage-cutover.md` for transactional
correctness evidence and its coordinated usage ownership gate. Usage caller wiring, account lifecycle,
real Stripe test-mode parity, timeout/lock load, representative-data migration and
reverse-state rollback remain release gates. These local command tests do not
establish production readiness for the whole migration.
