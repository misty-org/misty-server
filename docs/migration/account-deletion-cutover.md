# Account deletion migration

Status: initiation, status lookup and durable queue foundations are implemented
and tested, but **the production entrypoint does not mount these routes or run
these jobs**. Isolated payments closure and its API bridge are implemented and
tested. Local access removal and durable retention scheduling are implemented;
remaining provider cleanup and final purge handlers are still required.
Go remains the live lifecycle owner. Do not
activate initiation alone: it would disable an account without a complete cleanup
path.

## Public compatibility and immediate revocation

The testable router preserves `POST /me/deletion` and
`POST /account/deletion/status`, including `/api` and `/v1` aliases. No SDK RPC or
public billing API is added. Initiation requires a full account session, the
current password and exact `DELETE` confirmation. Active shared Spaces owned by
the user produce the existing ownership conflict with Space IDs, names and member
counts. Default/nonshared owned Spaces are retained for the later cleanup stage.

Password verification runs outside SQL locks. The transaction locks affected
Spaces in ID order, then the account and exact session; it compares the verified
password hash again and rejects memberships added while those locks were being
acquired. Statement and lock deadlines are five and two seconds, and a 25-second
operation deadline is checked between mutations. Two concurrent initiation
operations are admitted per repository, with five attempts per account per minute
across aliases. Failed authorization, contention, expiry or cancellation rolls
back both the request and revocation. Session expiry is checked against database
wall time immediately before commit, using the exact deadline captured under its
row lock.

The successful 202 response includes the existing request shape and a random
256-bit status capability; only its SHA-256 hash is stored. The request retains
the existing 30-day deadline and starts in `processing`. Status lookup works
after sessions are revoked and also recognizes existing Go capabilities. It
returns no user ID, hash, internal cleanup owner, lease or provider credential.
Bodies are bounded to 4 KiB; public status has 16 in-flight database reads and a
60-per-minute client-IP limit shared across aliases. IP resolution uses the
configured trusted proxy boundary. Responses use `Cache-Control: no-store`.

Initiation atomically marks the user pending deletion; deletes all account/App
sessions, password-reset and website handoff tokens, old/new OAuth authorizations,
cloud/GitHub credential handoffs and GitHub setup states; supersedes pending
password-recovery jobs; revokes trusted devices and device pairs; removes pairing
and presence records; disables self-host membership and outstanding enrollment
invitations; disables owned personal agents and schedules; and cancels active
runs where the user is owner, requester, initiator, billing principal or agent
owner. Related worker/device leases, approvals and contexts are canceled. Other
members' unrelated work remains active. Usage reservations are not refunded here:
terminal accounting must settle actual consumption rather than mint allowance
while an already-dispatched operation can still report usage.

Each affected Space receives durable note/drawing ACL control records and the
existing realtime user-disconnect notification. Membership and shared content
remain for the local cleanup handler. Device state revocation does not itself
prove termination of already-established offline peer connections. Encrypted
provider credentials are deliberately retained for the provider cleanup handler;
they must not be erased before retryable remote revocation has a recorded outcome.

## Durable ownership and ordering

Migration 158 adds `account_deletion_requests.cleanup_owner`, defaulting existing
and legacy-created requests to `go`, and service-only RLS-protected
`account_deletion_steps`. Native requests explicitly select `native` and create
four steps in the same transaction as revocation:

| Step | Eligibility |
| --- | --- |
| payments | Native processing request and pending-deletion user |
| providers | Native processing request and pending-deletion user |
| local | Both payments and providers completed |
| purge | Local completed, request scheduled and retention deadline reached |

Self-hosted initiation records payments as completed with the explicit
`not_applicable_self_hosted` outcome. All other steps remain pending. Claiming a
step never marks cleanup completed. Claims use `FOR UPDATE SKIP LOCKED`, a fresh
UUID fence and a two-minute lease. An expired lease can be reclaimed with a new
fence. Retries require the matching unexpired fence, retain the request and account
state, and back off from 15 seconds to one hour. Persisted failure codes are owned
by the module; raw provider errors and secrets are excluded.

The Go processing/purge selectors and failure/schedule/completion mutations now
exclude native requests. Existing Go-owned requests are **not imported or adopted**
by the migration. Applying migration 158 and rolling out these guards is necessary
before any mixed ownership rehearsal. An older running Go binary ignores the new
owner field; it must be drained before native initiation is enabled. The forward-only
migration retains cleanup evidence during application rollback. Restoring a Go
owner requires an explicit reviewed handover, never silently deleting queue rows.

## Remaining implementation and release gates

1. Signed closure, checkout/portal freezing, canonical session/subscription/customer
   cleanup and inactive entitlement receipts are implemented in isolated payments.
   The API bridge polls `closing` and acknowledges only an exact `closed` response
   under current account/request/lease fences. Its seven integration tests include
   the real signed roundtrip, lost responses and stale-worker races; see
   `billing-account-closure.md`. Stripe code and credentials stay in payments.
   Complete real-provider/import/rollback rehearsal before production activation.
2. Implement bounded provider revocation for both current connected accounts and
   legacy cloud/Space credentials, plus webhook/integration dependencies. Record
   provider-specific outcomes and retain retry material until resolved.
   Compatible credential readers and bounded remote-effect adapters have nine unit
   tests and five shared Go/TypeScript fixtures. Migration 159 and the durable
   provider worker add ten restricted-role database tests for resource inventory,
   dependency ordering and fenced acknowledgement. Migration 160 and six more
   database cases cover checkpointed Dropbox refresh/revocation recovery. Remaining
   credential refresh, provider-specific
   subscriptions, remaining policies and composition are unfinished. See
   `account-deletion-providers.md`.
3. Local membership cleanup, owned/default-Space scheduling, avatar deletion
   intents and App retention coordination are implemented in one fenced
   transaction. Migrations 161–162 retain default-Space protection while allowing
   eligible native cleanup. Ten database cases cover rollback, contention,
   ownership, retention and stale workers; see `account-deletion-local.md`.
   Complete end-to-end object/collaboration delivery and large-account rehearsal.
4. Implement the retention purge across current agent/app/provider/private account
   data while preserving required shared attribution and accounting evidence.
   Completion must follow these effects atomically, not just a successful claim.
   The private AI/agent SQL phase is implemented with seven restricted-role tests;
   it deliberately leaves purge pending. See `account-deletion-agent-purge.md`.
5. Inventory and explicitly import existing in-flight Go requests after draining
   its workers. Verify representative hosted/self-host deletion, provider failure,
   delayed payment events, restart/lease loss, rollback, resource limits, realtime
   disconnect and client status handling before mounting the production router.

Thirteen restricted-role PostgreSQL tests currently cover confirmation/password,
App and origin boundaries, ownership blockers, retention/capability compatibility,
self-host step selection, broad credential/device/run revocation, isolation from
other members, password/session races, full transaction rollback, real Space lock
contention, alias admission/rate limits, concurrent initiation, queue dependency
ordering and stale-worker retry fences. Separate Go tests cover the existing
lifecycle and exclusion of native requests from legacy processing and purge.
