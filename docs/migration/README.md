# TypeScript migration ledger

Status: active. The Go API remains the deployed authority until each replacement
passes parity checks. A Hono foundation is not a completed server migration.

See [completion-checklist.md](completion-checklist.md) for the finite required
deliverables, completion checks, dependencies, deferred improvements and the next
bounded work item. Report against that checklist before expanding work. The current
[registration checkpoint](registration-checkpoint.md) separates SDK coverage from
remaining REST routes, startup jobs and command obligations.

## Acceptance stages

- [ ] Baseline: routes, jobs, commands, SQL migrations, protocol fixtures, runtime measurements.
- [ ] Public SDK/contracts: coordinate with task `01a06e70-941f-7fb0-a818-408c0c1ddb40`; preserve host-mediated protocol and capabilities.
- [ ] Payments: independent process, billing schema/role, Stripe verification, inbox/outbox, reconciliation, backfill and rollback rehearsal.
- [ ] Hono API: all domain routes, authentication, RLS, authorization, transfers, realtime, integrations, jobs and admin commands.
- [x] Runtime organization: independent applications for agent runtime, managed collaboration and self-host collaboration; update CLI/build/deploy paths and validate local builds.
- [ ] Go retirement: zero production Go services, commands or build dependencies; keep historical parity evidence.
- [ ] Release readiness: hosted/self-host integration, migration/rollback, secrets boundaries, load results, observability and release instructions.

## Structure and conventions

`apps/api` owns HTTP composition and domain modules. Each domain has a small
`routes.ts` router, `service.ts` business behavior and `repository.ts` SQL boundary
only where those distinctions are useful. Name files in kebab-case, exported
types in PascalCase and functions in camelCase. Avoid one-file-per-function
fragmentation and large generic controller classes. Keep inferred route handlers
inline with Hono route definitions and compose routers with `app.route()`.

`apps/payments` owns payment processing and its schema. `packages` contains only
private infrastructure and shared subscription policy used by the API and
operator handover validation. Public SDK definitions live
in the separately distributable SDK repository; never export billing definitions
or private service credentials through that package.

Independent runtimes now live in `apps/agent-runtime`, `apps/journal-collab`, and
`apps/self-host-collab`. They retain independent dependency locks and deployment
lifecycles. API and payments are the root npm workspaces. Public, npm-packaged SDK
contracts are snapshotted in `third-party/misty-contracts`; the root must not have
a `vendor` directory while Go remains, because Go treats it as its dependency tree.

Use strict TypeScript, explicit dependencies passed to application factories,
runtime input validation, parameterized SQL, transaction-local RLS settings,
bounded outbound calls, and graceful shutdown. HTTP factories must be testable
without opening sockets. Unit tests are colocated; PostgreSQL integration tests
use isolated test databases and exercise real role/transaction behavior.

The new API uses port 8082 during development to avoid replacing the active Go
API before parity is verified. Liveness is separate from readiness. Readiness
must not claim full migration while required routes/jobs remain owned by Go.

## Compatibility

Preserve `/v1`, `/api`, supported bare paths, sessions, IDs, existing database
migrations, app RPC envelopes and method names, errors, pagination, deadlines,
streaming and self-host behavior. Stage ownership changes explicitly; never
fallback/retry a mutating request across implementations. One owner per job.

Payments owns Stripe records and subscription-derived entitlements. The API owns
account identity, trials, lifetime grants, usage/allowance transactions, access
enforcement and self-host proof issuance. Website-only billing UI does not replace
server-side rejection of downloaded-app credentials.

## Current evidence

- Initial source inventory: 529 Go implementation files, 222 Go test files,
  143 SQL migrations; counts may change with concurrent SDK work.
- Existing agent runtime image targets Node 24; retain that runtime baseline.
- Existing PostgreSQL authorization uses transaction-local `app.*` RLS settings.
- Existing SDK registry allows named Space-scoped methods, not arbitrary URLs.
- Public SDK task notified of the approved contract/extraction boundary.
- Production/staging deployment and public package publication are release
  actions; no claim of live verification is made by local tests.

### Verified local implementation, 2026-09-05

- Hono factories, strict TypeScript, request correlation, sanitized errors/logs,
  explicit environment composition, SQL pools, transaction-local RLS, and shutdown.
- All 168 application migrations apply to isolated PostgreSQL. Separate billing
  migration history, concurrency locks, checksums, rollback and repeat execution
  pass integration tests. Existing Goose history is retained; never run both
  migration engines concurrently during a release.
- Installed-app sessions and account/app-scoped personal records have native
  repositories and handlers. The optional RPC gateway validates the shared
  protocol and capabilities; native dispatch is enabled for twenty Journal
  methods, `spaces.get`, `spaces.members.list`, `tasks.list`, `tasks.activity.list`, `tasks.create`, `tasks.update`, `tasks.delete`, `tasks.move`,
  `connections.list`, `connections.remove`, `connections.authorize`, `mail.accounts.list`, `mail.folders.list`,
  `mail.threads.list`, `mail.threads.get`, `mail.threads.action`,
  `mail.drafts.create`, `mail.drafts.update` and `mail.drafts.send`
  along with `calendar.events.list`, `calendar.events.create`,
  `calendar.events.update`, `calendar.events.delete` and `agenda.list`
  plus `calendar.sources.list`, `calendar.sources.create`, `calendar.sources.delete`,
  `calendar.google.calendars` and `calendar.sync`
  plus `roadmaps.list`, `roadmaps.create`, `roadmaps.get`, `roadmaps.update` and `roadmaps.delete`
  plus all Roadmap milestone/goal/node/definition/edge/layout methods, goal task-link
  replacement and integration list/bind through a shared native dispatcher
  (all74 public HTTP methods). Non-SDK REST routes and background/runtime
  integration still require migration; Hono does not forward requests to Go. Task automation/assigned-Agent effects
  are persisted by the native effect consumer as runs/jobs and pending workflow
  claims; actual Agent/workflow execution remains incomplete. Handler coverage is
  not complete background execution. See `planner-cutover.md` and `task-effects.md`.
- Native connected-account list/removal preserve public metadata, ownership and
  scopes, Go-compatible credential encryption, local erasure and bounded provider
  revocation/Figma cleanup. See `connections-cutover.md` for concurrency tradeoffs
  and `connection-authorization.md` for native OAuth state, consent and release gates. Native Inbox account/folder/thread
  reads now use the private refresh broker. Thread actions commit content-free
  audit intent before provider writes and serialize each write with revocation.
  Draft edits/sends share a database lock, committed audit intent and bounded body
  admission; see `mail-cutover.md` and `mail-draft-adapters.md` for provider bounds
  and remaining provider/reconciliation/load gates. Browser 1.1.0 is synchronized
  from the reviewed SDK/Store catalog as a signed desktop download with permission
  version 2 and minimum host protocol 2; mobile remains embedded.
- The SDK task's tested 201-method public snapshot is synchronized, including
  native file transfers, directory handoff, saved-directory remember/reopen/forget/list
  and host-only workspace operations, plus `files.replaceCopy`, `files.openExternal` and `files.listArchive`. Public HTTP remains
  74 methods and billing remains private. Snapshot integrity is recorded in
  `third-party/misty-contracts/.snapshot.json`.
  The reviewed workspace revision archive is synchronized: snapshots/events now
  carry a nonnegative safe-integer revision, and workspace-state validation accepts
  repeated object references while rejecting cycles. The SDK task verified its
  packed public consumer and host/app integration; no HTTP methods were added.
- Public SDK registry: 74 methods, with Go registry synchronization and an
  exhaustive Hono capability map. Go tests verify official catalog grants,
  bound-Space denial and empty-scope denial for every method and path alias.
- Payments: raw-byte Stripe signatures, durable deduplicated webhook inbox,
  fenced worker leases, transactionally coupled state/outbox updates, canonical
  subscription reads serialized per account, configured-price/identity checks,
  and reconciliation with bounded retry backoff.
- Private Ed25519 service assertions bind issuer, audience, subject, scope,
  method, path and body. Entitlement outbox delivery and API projection inbox
  tolerate duplicate deliveries, lost acknowledgements and out-of-order revisions.
  The native consumer updates license/trial state and the personal weekly wallet
  in that transaction. The API mounts it when hosted payment verification keys
  are configured. Trial history, usage and live reservations survive activation.
  A bounded indexed expiry worker handles trial deadlines and the existing
  72-hour subscription fail-safe without changing canonical payment state.
- Legacy checkout import now preserves original Go attempts in a protected archive.
  Recovery verifies known sessions or persists a complete bounded-window scan;
  ambiguous identity stays blocked for operator review. Thirteen database tests
  cover import, recovery, concurrency, rollback and command barriers. Payments
  has paused, delivery-only and active startup modes; API expiry is separately
  opt-in. See `legacy-checkout-cutover.md` for remaining live/rollback gates.
- Website checkout and portal routes now call the isolated payments service with
  signed, bounded commands under account/license/session locks. Payments rechecks
  its history before offering a Pro trial. Retired trial/add-on endpoints preserve
  410 responses; App credentials remain rejected. Eleven database/HTTP cases
  verify aliases, eligibility, loss/retry, lock/session races and bounded admission.
  See `website-billing-cutover.md`; release rehearsal remains.
- Native billing usage now composes storage, personal/Space wallets and private
  subscription metadata under one local accounting transaction. Eleven database
  tests and eight shared Go/TS fixtures verify accounting, authorization, rollback
  and concurrency; see `billing-usage-cutover.md` for its coordinated ownership gate.
- Private billing closure now persists a permanent admission tombstone and an
  ordered null entitlement. Checkout/portal operations serialize with closure,
  and inactive API accounts retain delivery/reversal evidence without renewed
  access or allowance. Ten billing and two API cases cover these boundaries.
  Canonical cleanup has fourteen further database cases, including actual database
  connection loss during a simulated provider effect. The active payments mode
  mounts the private closure command and worker; the API bridge has seven database
  cases proving signed roundtrip and fenced acknowledgement. Public deletion
  remains unmounted pending remaining provider/purge stages and release rehearsal;
  see `billing-account-closure.md`. Provider cleanup now reads both credential
  generations with Go-compatible fixtures and bounded remote-effect adapters;
  durable provider inventory and fenced acknowledgement now have ten database
  cases. Dropbox refresh/revocation recovery adds six cases with encrypted
  checkpoints and exact OAuth client identity. Other credential refresh,
  remaining provider handlers and activation remain. See
  `account-deletion-providers.md`.
- Native local account cleanup removes membership while preserving shared content,
  schedules owned/default Spaces without shortening existing retention, and
  queues avatar and private App cleanup. Ten restricted-role database cases cover
  prerequisites, default protection, real contention, expired leases, atomic
  rollback and existing purge attempts. Production composition remains gated;
  see `account-deletion-local.md`.
- Migration 163 preserves both AI attachment image keys through metadata deletion,
  replacement and conversation/account cascades. Storage cleanup protects live
  attachment references. Five new cases plus avatar/Journal regressions (35 tests)
  and three migration-safety/history cases pass. This targeted pass is separate
  from the last full-suite and image evidence below.
- Private AI/agent retention purge now has a fenced transaction and seven targeted
  database cases. It removes private payloads/memory, redacts owned definitions,
  preserves shared attribution/accounting and queues attachment cleanup. It records
  only a phase receipt; account completion remains unimplemented. See
  `account-deletion-agent-purge.md` and the bounded completion checklist.
- Private account settings/history purge shares those transaction fences. Five
  new cases plus seven agent-phase regressions pass with typecheck/build. The
  direct/composite account-reference inventory records 183 entries, including 76
  still requiring another cleanup policy; it is not a blanket deletion plan.
  See `account-deletion-private-state.md`. Overall R2 remains incomplete.
- Account disable now cancels Misty AI admission/settings and recap scheduling
  while retaining unresolved effect evidence. Native callback persistence checks
  lifecycle/runtime identity and uses migration 164 receipts for exact replay.
  The signed HTTP/runtime composition remains incomplete. Forty-three targeted
  database cases and typecheck/build pass; see `misty-lifecycle.md`.
- Native Misty invocation creation/activation repositories now enforce exact
  sessions, account/AI state, Space/conversation access, idempotency and one
  runtime binding. Twelve targeted database cases and typecheck/build pass.
  HTTP ingress, provider dispatch and usage integration remain incomplete;
  see `misty-admission.md` for scope and the stricter idempotency conflict rule.
- Restricted PostgreSQL role tests reject cross-service table access and
  privileged runtime identities. Role audit is also used by payments workers and
  runtime readiness; production role provisioning/backfill remains outstanding.
- 204 unit tests and 350 PostgreSQL integration tests pass; the migration suite includes a fresh
  database, upgraded through all 162 application migrations and ten billing migrations.
  CI runs these tests in its own
  database, separate from Go's integration tests.
- Node 24 API and payments images build independently. Local image smoke verifies
  non-root operation with a read-only filesystem, no external network, liveness,
  honest unready status, and zero-exit graceful shutdown. The API image contains
  neither Stripe nor the payments application. See `image-smoke.json` for the
  exact tested image IDs; source changes require a new smoke run.
- Moved runtime checks pass: agent typecheck/22 tests/build, managed collaboration
  typecheck/30 unit tests/4 real-runtime lifecycle tests/dry-run bundle, self-host
  collaboration integration test, CLI compilation/53 tests and the existing
  container contract. Agent and self-host images rebuilt and passed isolated
  Linux startup/health/non-root/shutdown checks (`runtime-image-smoke.json`). The
  installed CLI was refreshed; its previous binary is preserved as
  `~/.cargo/bin/misty.before-hono-migration`.
- The collaboration toolchain audit now reports zero vulnerabilities after the
  Vitest patch update and targeted Miniflare dependency patches. Wrangler remains
  on its compatible major-types baseline: the latest toolchain requires Workers
  types v5, which the current y-partyserver peer declaration does not support.
- Checkout service/repository tests cover concurrent requests, immutable Stripe
  parameters on retries, completed sessions awaiting webhooks, past-due recovery,
  and orphaned-session recovery with durable pagination. Private checkout and
  portal HTTP commands require request-bound API service assertions; tests reject
  account cookies, downloaded-app tokens, different subjects/actions and injected
  price/return-URL parameters. The API-owned trial decision and public website
  facade are integrated; representative handover and provider verification remain.
- Legacy purchases have an operator-only dry-run/commit import that preserves the
  exact source records, rejects conflicting identities/content and checkpoints
  only the verified transaction. Refund/dispute processing waits for that import
  and emits one irreversible reversal per purchase. Tests cover import rollback,
  unchanged historical license tiers, payment-intent recovery, duplicate disputes,
  delayed reversal after a newer subscription and failed consumer retry.
  API lifetime grants now retain source attribution. Historical tiers are copied
  verbatim as unattributed grants; refunds wait for a verified purchase mapping.
  Native reversal tests preserve independent manual and paid-subscription access.
- Synced signed Terminal 1.1.0, Planner 1.1.0 and the latest public SDK snapshot
  (74 HTTP routes, including six Journal asset operations, eight Inbox mail methods
  and native connection authorization). HTTP/database verification confirms stale permission consent is
  rejected without changing grants and a reviewed upgrade retires old sessions,
  including Planner's permission 3→4 transition. Planner remains embedded on mobile.
  The SDK task's latest Terminal focus-fix rebuild still needs its unlocked
  desktop rerun; server/catalog tests do not establish that UI result.
- Real Planner RPC business checks pass twice against isolated PostgreSQL:
  task create/list/update/move/archive preserves last-write-wins and tombstones;
  calendar and roadmap edits enforce version conflicts; roadmap node/layout
  changes persist; bound-Space and reviewed write scopes remain mandatory.
- Native usage reservation, settlement, release, refund and lease renewal now
  cover both personal and Space wallets. Separate consumption counters preserve
  spent allowance through downgrade/reupgrade, generation checks fence stale
  completions, and period-bound refunds cannot refill a different week's usage.
  See `usage-cutover.md` for correctness evidence and remaining integration gates.
- Native registration/login/logout and browser handoff now mount under all three
  aliases. Existing Go passwords/sessions remain compatible, authentication work
  and requests are bounded, and transaction tests cover stale credential races,
  handoff replay/rollback and subject-bound self-host login proofs. See
  `auth-cutover.md` for the implemented subset and remaining authentication gates.
- Password recovery now uses a durable, leased background delivery queue with
  stable retry tokens, private rotating token keys, Mailjet transport bounds,
  and atomic account/app-session revocation. Existing Go reset tokens remain
  compatible. Native cleanup removes expired credentials and old delivery jobs
  in bounded batches. Email identity uniqueness now matches login normalization.
- Instance discovery and self-host bootstrap/enrollment/invitations/renewal now
  have native implementations. Restricted-role tests cover first-admin races,
  atomic creation/consumption, expired proofs and app/account separation. Native
  self-host admin commands handle bootstrap tokens, password reset and disabling.
  Hosted features remain unavailable on self-hosted deployments, which start
  without hosted email secrets. Both API deployment modes pass local image smoke.
- Hosted self-host proof issuance now reads API licenses/payment projections,
  preserving Go signing formats and stable account subjects without Stripe access.
  Both runtimes verify compatibility fixtures; tests cover payment eligibility,
  expiry, account/app separation, RLS isolation and unavailable signers. Existing
  subscription backfill, signing-secret provisioning and live renewal parity are
  still required before changing endpoint ownership.
- Native profile/device/preferences and telemetry routes preserve existing fields
  and aliases, with account-only authorization and active-state checks. Account
  and Space-member avatar routes now use immutable publication, bounded S3 or
  Go-compatible self-host filesystem storage, and durable cleanup. Eleven database
  tests, shared PNG fixtures and actual Go/native filesystem round trips pass;
  Linux image smoke includes the local storage path. See `avatars-cutover.md` for
  ownership and rollback gates. Native `/me` now reads API license state and a
  signed private payments summary; seven cross-service database/HTTP tests cover
  history completeness and account/session rechecks. A separate operator import
  copies subscription/customer history with exact snapshots and atomic rollback.
  The initial entitlement command now verifies license consistency and queues each
  subscription snapshot once; signed delivery preserves trial history and used
  credits. Seven database tests include acknowledgement loss, rollback,
  concurrent reconciliation and a 101-account batch. See
  `account-summary-cutover.md` and `initial-entitlements-cutover.md`; representative
  handover rehearsal, usage caller integration, legacy checkout recovery rehearsal,
  personal-agent avatars and account deletion remain pending.
- Native account export now preserves the format-v2 manifest with password/session
  rechecks, current resource access and signed viewer/asset downloads. A private
  unlinked file bounds manifest heap use; eleven database tests cover authorization,
  streaming, concurrency and failure cleanup. See `account-export-cutover.md` for
  response limits, intentional access fixes and remaining release gates.
- Account deletion initiation/status and durable cleanup queues now have native
  factories with explicit Go/native ownership, immediate authorization/device/run
  revocation, 30-day retention, ordered stages and stale-worker retry fencing.
  Thirteen PostgreSQL cases and separate Go compatibility tests cover this
  foundation. Production routes/jobs remain unmounted until payments/provider,
  local cleanup and purge handlers are complete; see `account-deletion-cutover.md`.
- Official catalog, installation/upgrade/pinning, uninstall/recovery scheduling
  and app-session issuance now have native implementations. Atomic permission
  changes and uninstall revoke old tokens; record operations revalidate the exact
  credential under locks. Catalog snapshots and CI update both Go and Hono from
  the reviewed Store source. See `apps-cutover.md`; remaining domain RPC dispatch is incomplete. The native
  private-app purge worker now has fenced claims, transaction rollback, namespace
  isolation and restore/claim concurrency tests; its ownership switch defaults off.
- Native onboarding and Space foundations now create protected defaults and the
  reviewed five apps atomically; ordinary creation supports all six templates.
  Space list/read/rename/setup, current app credential checks, owned-Space quota
  contention and retry rollback pass restricted PostgreSQL tests. Go/native
  fingerprints and template catalogs agree. Native member/agent reads protect
  owner-only agent details; owner permission changes preserve dependency rules.
  Invitations now support durable delivery, resend/revoke, public preview and
  single-use acceptance/decline, including existing Go-issued links. See
  `spaces-cutover.md` and `invitations-cutover.md` for remaining lifecycle,
  delivery and integration gates.
- Native member removal/leave now cancels the affected agent/device work and
  schedules, preserves shared content and atomically queues both note and drawing
  ACL revocation. Ownership transfer serializes plan capacity with creation,
  reclaims expired AI leases, blocks live reservations and preserves consumed
  allowance across owner changes. Eight restricted database lifecycle tests cover
  races, rollback, account/app boundaries and committed control notifications.
  Complete Space deletion/recovery, realtime consumption and production revocation latency
  remain outstanding.
- Native Space deletion requests preserve exact confirmation and the 30-day
  deadline, cancel active Space work and retain recoverable content. Notifications
  are byte-bounded and verified against a 303-member Space. Source review found
  that Go never implemented permanent Space cleanup or recovery; those remain
  explicit native lifecycle gates rather than an assumed existing worker.
- Native Planner task queries and activity reads preserve filters/cursors/status
  totals and private conversation visibility, including current app and member
  permission checks. SDK query forwarding and precise timestamp bounds pass
  real database tests. Native task writes, Calendar events/agenda and explicit Google
  source synchronization are now implemented. Watch/callback reconciliation now has native ownership and a leased worker.
  Legacy source handover and complete Agent/workflow execution remain open. See `planner-cutover.md` and `calendar-sources.md`.
- Synced eight public Inbox contracts with explicit mail.read/mail.write mappings,
  narrow provider-ID encoding and bounded draft envelopes in both Go and Hono.
  The existing Go thread handler's double decoding is corrected; real Chi/domain
  tests pass opaque plus/slash/percent IDs and a 4 MiB draft attachment to a fake
  provider without sending mail. Native Hono mail handlers are implemented;
  see `mail-cutover.md` for compatibility evidence and remaining implementation.
- Native Journal implements twenty note/drawing methods and signed note
  projections with transactional audience/session checks, backlinks and control
  outboxes. Native-issued tickets pass real managed-worker WebSocket checks for
  editor/viewer behavior, replay and ACL revocation. See `journal-cutover.md` for
  evidence, deliberate authorization improvements and outstanding self-host
  persistence and broader lifecycle requirements. Native control/purge workers
  have bounded signed delivery, fenced retries and metadata cleanup gates.
- Native Journal asset transfers now reserve personal/Space quota, sign bounded
  direct S3 operations, verify immutable upload metadata outside transactions,
  and atomically finalize files/assets/contributions/events. Tests parse all six
  SDK responses and cover concurrent retry, cross-Space quota contention,
  revocation, mismatch, deduplication repair and rollback. Abandoned-upload and
  durable object-deletion and asset-retention workers are behind an explicit
  ownership switch. Retention preserves shared blobs and held resources while
  releasing each logical contribution once.
  See `storage-cutover.md`; live R2 and full storage lifecycle parity remain open.
- The real SDK host clients pass a local network run through Hono/PostgreSQL and
  HTTPS object storage: multi-chunk uploads/downloads, verified hashes, corrupt
  download denial, revocation between PUT/finalize, and view-lifetime cleanup.
  `journal-network-probe.json` records the evidence and successful fixture cleanup.
  This does not establish production R2 or native desktop verification.

### Remaining work before any ownership switch

Payments still requires coordinated website/usage ownership handover,
hosted-AI caller integration, account lifecycle,
restricted-role provisioning, reviewed grant attribution and other backfills,
observability and a rollback rehearsal. Legacy reversal webhooks stay retryable
in the new inbox; they are not silently consumed by an incomplete processor.

The main API still requires native domain handlers, the remaining authentication/session flows,
Space and object authorization, Misty/agent modules, library/transfer/realtime and
integration protocols, background workers and administrative commands. Go remains
the live owner of these operations. Neither Hono service is release-ready and
neither readiness flag may be enabled on the strength of scaffold tests.

Then complete hosted/self-host parity, Linux runtime image tests, failure/load
testing, deployment/rollback artifacts and Go retirement. No Go server runtime,
commands or build dependencies have been retired yet.

### Reproduce verification

Run `npm run check` for registry consistency, type checks, unit tests and build.
Run `npm run test:integration` with `MISTY_TEST_DATABASE_URL` pointing explicitly
to a disposable database whose name ends in `_test`; normal app credentials are
never inferred. Run `node scripts/migration/smoke-images.mjs` to build and test the
isolated images; it removes only its own containers and temporary test key.

`npm run contracts:sync` snapshots the built sibling public contracts package and
updates the temporary Go registry. `npm run contracts:check` compares against the
built public source package. Normal CI builds require no private SDK checkout.
When the SDK task is already building its next version, verify the supplied archive
hash and use `node scripts/migration/sync-contracts.mjs --archive /absolute/reviewed.tgz`,
then `node scripts/migration/sync-go-contracts.mjs`. The snapshot check accepts
`--check --archive /absolute/reviewed.tgz` too. This path is verified against the
exact reviewed archives. The current 200-method contracts archive has SHA-256
`96a5eccadd380a5a8bd1d567d270297f413bb29ae5620af33bd927b8d3d93ba0`.
The SDK task verified the public package/consumer and Files editing methods;
those two new native-only methods do not change the HTTP contract. Files catalog
scopes remain unchanged until its separately reviewed promotion.

Generate source inventory with `npm run migration:inventory`. Registration
expressions are discovery candidates, not a proof of effective mounted route
coverage; runtime parity fixtures must cover composition and aliases.
