# Payments cutover work ledger

This is an active implementation ledger, not authorization to change live billing
ownership. Go still owns live billing. The new payments service remains unready.

## Implemented and locally tested

- Separate Node/Hono process and Stripe-only runtime image; the API runtime image
  excludes Stripe and the payments application.
- Separate billing schema and migration history; runtime role audit rejects
  privileged identities and application-table grants.
- Durable signed-webhook inbox with deduplication, lease recovery and fencing.
- Canonical subscription validation and account-serialized reconciliation;
  durable entitlement outbox coupled to state changes in one transaction.
- Private, signed entitlement delivery with an API inbox and revision ordering.
- Native API license/trial and personal weekly-wallet effects commit with that
  inbox. Independent consumption counters preserve spent usage through repeated
  plan changes; duplicate events do not refill it. An indexed, bounded API expiry worker
  handles trial deadlines and the 72-hour missed-reconciliation fail-safe while
  payments keeps its canonical status and duplicate-checkout protection.
- Source-attributed API lifetime grants preserve historical license tiers. A
  verified purchase reversal revokes only its grant and recomputes remaining
  lifetime/subscription access. Missing attribution remains retryable.
- Checkout creation persists a unique account attempt and exact Stripe request
  parameters before sending. Retries retain the idempotency key and parameters.
- Old unresolved attempts are recovered by bounded pages over a fixed Stripe
  creation window. A durable cursor survives worker restarts; network errors
  retain the attempt and apply backoff. Existing Go attempts lack the new attempt
  metadata; the separate legacy importer and recovery worker now preserve them
  without replay. See `legacy-checkout-cutover.md` for pagination, review and gates.
- Portal sessions resolve the customer from the authenticated account's billing
  record and use the configured return URL.
- `/internal/billing/checkout` and `/internal/billing/portal` require assertions
  from `misty-api`, addressed to `misty-payments`, bound to the affected user,
  action, path, method and exact body. They accept no browser or app credentials.
- `/internal/billing/summary` now provides the native account summary through the
  same private assertion boundary with its own read scope. Missing import history
  returns unavailable instead of falsely granting trial eligibility. Subscription
  and customer import, original-record snapshots and role/rollback tests are
  implemented; see `account-summary-cutover.md` for configuration and remaining
  entitlement handover and checkout recovery gates.
- Initial subscription snapshots now have a separate operator dry-run/commit
  command, state-conflict checks, a durable once-only producer marker and
  acknowledgement/projection counts. Signed-delivery tests preserve existing
  trial and usage state; see `initial-entitlements-cutover.md`.

Stripe's [idempotency contract](https://docs.stripe.com/api/idempotent_requests)
requires identical parameters and permits key pruning after 24 hours. The new
service stops blind creation retries before that window and recovers the original
session instead. Checkout expiry follows Stripe's
[session creation contract](https://docs.stripe.com/api/checkout/sessions/create).

- Durable private account closure, account-locked checkout/portal admission,
  suppression of late paid entitlements and inactive-account delivery/reversal
  handling are tested. Active mode now mounts canonical resource cleanup and its
  private command. The API acknowledgement bridge is tested but public deletion
  stays unmounted pending provider/local/purge stages; see `billing-account-closure.md`.

## Remaining implementation

1. Rehearse the implemented legacy-purchase import on a representative database
   copy, then perform it during the final ownership switch. The operator command
   `npm run import:legacy-purchases` is read-only by default; `-- --commit` requires
   Go billing writers to be stopped first and remain stopped. It copies purchase
   IDs, customer/payment/charge mappings, status, amounts, currency and timestamps
   verbatim, rejects conflicting targets, verifies the copy, and atomically writes
   `legacy_purchases_imported`. It creates account/license associations but does
   not infer account customer preference or change license tiers. The separate
   `import:legacy-subscriptions` command now copies subscriptions and verifies
   Go-compatible customer selection. Rehearse both imports, initial entitlement
   projection delivery and unresolved-checkout backfill before changing ownership.
2. Refund/dispute consumption now waits for that checkpoint, changes the purchase
   and enqueues a private reversal in the webhook completion transaction. API
   deduplication is per purchase and applies an older reversal even after a newer
   subscription snapshot. These paths and the native grant consumer pass isolated
   PostgreSQL tests; the reviewed attribution import remains outstanding. Keep retired one-time checkout grants
   disabled. API-owned lifetime
   grants must be distinguished from purchase-derived grants so a payment event
   does not accidentally revoke unrelated access.
   Historical SQL promoted some legacy tiers differently from the current
   `legacyTierFromMetadata` helper; the import must preserve the recorded license
   grant rather than infer its tier again from an old purchase string. Prefer a
   private irreversible purchase-reversal message, deduplicated per purchase,
   alongside replaceable subscription snapshots. A later subscription revision
   must never cause an earlier unprocessed reversal to be discarded.
3. Wire the implemented native usage reservation, settlement, release/refund and
   Space-wallet paths into all hosted-AI callers. Billing usage is integrated. Tests verify
   downgrade/reupgrade consumption, in-flight reservations and refunds; the old
   clamped-balance bug is fixed for native operations. Follow `usage-cutover.md`
   for historical period attribution, generation/lease handover and load gates.
4. API account authentication, account summary and website checkout/portal commands
   are native. API local trial eligibility and payment-history rechecks serialize
   under their respective account locks; retired local-trial/add-on routes retain
   410 responses. See `website-billing-cutover.md` and `billing-usage-cutover.md`.
   Rehearse provider latency, contention, Stripe test-mode parity and rollback.
5. Provision restricted billing credentials using migration credentials; retain
   migration-table ownership outside the runtime role. Add an auditable backfill
   command, read-only validation report and reconciliation checkpoint. The purchase,
   subscription, initial entitlement and legacy checkout commands now exist;
   provisioning and representative-data rehearsal remain.
6. Handle account deletion, Stripe customer lifecycle, reconciliation expiry
   (including the existing 72-hour fail-safe), retention, telemetry, queue metrics
   and alerts, and rehearse imported unresolved checkout recovery.
7. Rehearse the ownership switch and rollback on a copy of the database with
   webhook duplication/out-of-order delivery and outages. Only one implementation
   may create checkout sessions or own each job at a time. Preserve `/stripe/webhook`
   and any configured static webhook URL path through routing at cutover.

## Configuration for the separate process

Runtime credentials use `BILLING_DB_*`; migrations require separate
`BILLING_DB_MIGRATION_USER` and `BILLING_DB_MIGRATION_PASSWORD`. Stripe secrets and
the four configured price IDs belong only to payments. Checkout and portal return
URLs are configured by the operator, never supplied by SDK requests.

`PAYMENTS_SIGNING_KEY_FILE` is an Ed25519 PKCS#8 private key, identified by
`PAYMENTS_SIGNING_KEY_ID`. `API_VERIFICATION_KEYS_FILE` is a JSON array of
`{ "id": "key-id", "publicKey": "SPKI PEM" }` entries for API request verification.
`API_ENTITLEMENTS_URL` is the trusted API receiver URL ending in
`/internal/payments/entitlements`. Production service calls require HTTPS.
The hosted API loads `PAYMENTS_VERIFICATION_KEYS_FILE` in the same public-key
array format to mount its native receiver; expiry is separately opt-in. Self-hosted startup
rejects that configuration. During migration, omitting it leaves that receiver
disabled; readiness remains false regardless.

Use independent signing keys for the two services and retain overlapping public
verification keys only for the bounded rotation period. None of these files or
private service contracts belongs in the open-source SDK package.

Payments startup now uses explicit `PAYMENTS_MODE=paused|delivery|active` ownership.
The default is paused. Delivery mode starts only the entitlement dispatcher; active
mode enables provider workers and private checkout/portal commands. API entitlement
expiry separately requires `MISTY_NATIVE_ENTITLEMENT_EXPIRY_JOBS=1`. See
`legacy-checkout-cutover.md` before changing those settings.
