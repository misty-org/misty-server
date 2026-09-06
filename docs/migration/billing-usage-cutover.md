# Native billing usage

The account-only `GET /billing/usage` route and its `/api` and `/v1` aliases now
compose personal storage, personal and Space AI wallets, API entitlements, trial
status and private subscription metadata. This is a website/account API endpoint;
it adds no billing method or private contract to the public SDK. Self-host mode
retains the existing 501 feature gate.

## Response and accounting

The response retains `plan`, `storage`, `entitlements`, `personal`, `spaces`,
`agent_usage`, `hosted_ai` and optional `subscription`/`trial`. AI amounts and ratios
preserve Go's balance-derived display, including over-reserved and over-credited
wallets. Native consumption counters independently prevent plan changes from
refilling spent allowance. The eight shared fixtures in
`fixtures/billing-ai-usage.json` execute against both Go formatters and TypeScript.
Timestamps represent the same instants; JavaScript serializes millisecond precision.

Personal storage counts the caller's active/recovery contributions and active
upload/rendition reservations across active Spaces, including retained contributions
after leaving a Space. Released reservations and deleted Spaces are excluded.
Storage expiry remains the storage worker's responsibility; a usage read does not
independently release upload capacity. Space summaries include current memberships
in updated-time order and use each owner's storage/AI limits. Only an owner repairs
the derived Space storage counter from authoritative ledgers, with its version
incremented only when the totals change. A member reads the existing counter.
Nested personal/Space totals and the older personal-mirroring flat fields remain.
Unsafe JSON integer precision fails the whole transaction instead of rounding bytes.

Weekly refresh and expired AI lease reclamation use the existing native usage
repositories. Reclamation releases both the Space reservation and each initiating
account's reserved balance, including another member's expired reservation. Renewed
leases survive regardless of their creation time. The caller's personal wallet is
read last so the response reflects all Space releases. An expired local trial falls
back to its recorded lifetime tier or Basic without deleting trial history. Private
subscription metadata cannot grant an API license or replenish usage.

## Authorization and transactions

The API validates its exact active account session before calling the signed
payments summary endpoint. No API transaction or account lock spans that network
call. Payments must have verified purchase/subscription import history; missing
history/client or private failure returns 503 before usage side effects occur.
The private response contains no Stripe customer/session identifiers in the public
result and is never used as the source of access entitlements.

The local transaction locks all joined active Space rows in ID order, then all
affected account rows in ID order, including expired reservation owners. It
rechecks the exact session, account/license identity and current membership before
permissions, storage and wallet work. Storage advisory keys also use a global
sorted order. This follows native usage and Space ownership lock order. The final
wall-clock session/cancellation check precedes commit. Denied storage permission
retains Go's whole-response 500 behavior. Any late database/precision failure rolls
back license expiry, reservation releases, wallet grants and counter repairs together.

Admission is shared across billing commands/usage and all aliases, with at most
two requests in flight per process. Both local transactions use a two-second lock
and five-second statement timeout. Cancellation and a 25-second operation deadline
are checked between phases/Space iterations and before commit; an already-running
SQL statement is bounded by its own timeout rather than forcibly interrupted.
The private summary client additionally has its five-second deadline and 8192-byte
response bound. Lock timeout, statement timeout, deadlock or cancellation becomes
503; there is no automatic replay. Pool acquisition uses the configured pool timeout.

## Verification and remaining gates

Eleven PostgreSQL tests connect the real API to a local payments HTTP listener with
signed requests and separate restricted roles. They cover complete/missing history,
App denial, aliases/cookies/self-host gating, global and per-Space storage, all three
storage ledgers, owner-only repair, subscription privacy, trial/weekly/plan changes,
live/stale AI reservations, permission denial, account/session/membership/ownership
rechecks, rollback, precision rejection, concurrent overlapping usage, admission,
cancellation and real SQL lock contention. The API role also needs the legacy
`owner_storage_usage` trigger privileges when Space storage counters change.

This endpoint writes wallets even though its HTTP method is GET. Do not switch it
to Hono while Go still writes the same usage state. Drain or explicitly hand over
all AI reservations, then transfer usage callers, billing usage, entitlement expiry
and relevant owner/account lifecycle operations together. Go's old usage paths do
not maintain native consumption generations/lease metadata.

The endpoint preserves the complete all-Spaces response. It does not silently
truncate memberships; large-account response memory, latency, multi-replica admission
and contention need representative load rehearsal. Subscription metadata is a
separate service snapshot and can lag the API's entitlement projection; there is
no distributed atomic read across services. Production import, real provider,
crash/rollback and full caller integration gates remain in `usage-cutover.md` and
`payments-cutover.md`. Go remains deployed and migration readiness remains false.
