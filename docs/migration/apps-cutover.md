# Native app management

The native API now serves the reviewed official catalog, installed-app list,
install/upgrade, pin/unpin, uninstall/recovery scheduling and five-minute app
credentials under the existing bare, `/api` and `/v1` paths. These routes require
full active account sessions; app credentials cannot inspect or manage account
installations. Go still owns deployed requests and the active data-purge worker.

## Catalog and contracts

`apps/api/src/modules/official-apps/catalog.generated.ts` is a build-time snapshot
of the reviewed Store catalog. Runtime validation and lookup live in `catalog.ts`.
The native image/build needs neither a Go program nor a sibling Store checkout.
Refresh both catalogs from the same reviewed `misty-apps/apps/catalog.json`:
run the Store's Go generator and then
`npm run catalog:sync -- ../misty-apps/apps/catalog.json` in this repository.
`npm run catalog:check` compares the native snapshot to the current deployed Go
catalog as a temporary migration gate; it does not promote an app.

The catalog-sync workflow now updates both files and checks the native build.
The ten-app snapshot includes Terminal, Planner, Browser and Journal as signed
desktop downloads; these apps remain embedded on mobile. The public HTTP SDK
registry contains 74 methods. The reviewed SDK snapshot now contains 198 methods,
including same-App/account/Space single-use directory handoffs (60-second expiry),
four macOS-only file transfer operations and the tested host workspace
snapshot/update/focus/close/place methods, workspace subscriptions and extended
workspace.open state/placement options confirmed by the SDK task. The final reviewed
archive adds monotonic workspace snapshot/event revisions so delayed replies cannot
overwrite newer host state; repeated JSON object references are accepted while
cycles are rejected. They add no HTTP endpoint or billing capability. The
saved-directory remember/reopen/forget/list methods are included in the reviewed
198-method archive; persistent folder identities stay in the Host's OS vault.
No provider or billing credentials enter these contracts. Native host/UI wiring and
app promotion remain coordinated with that task. The exact public package integrity
is recorded in `third-party/misty-contracts/.snapshot.json`. Permissions are copied only from the trusted catalog after the client
submits its reviewed permission version. Caller-supplied scope overrides reject.

## Transactions and revocation

Account state is locked before installation rows. A shared installation advisory
lock serializes concurrent install, uninstall and pin operations, including a
previously missing row. Installation state, grants, recovery jobs and audit events
commit together; an audit failure rolls back all of them. Reinstall during the
recovery window preserves private records and cancels the pending deletion job.
Once an installation is `purging`, reinstall/uninstall refuse to cross that
irreversible boundary. Purged installations are absent from the account list.

Session issuance locks the active account and installed grant before inserting
an opaque SHA-256-hashed credential. A supplied Space must be an active membership.
Embedded desktop apps cannot request isolated app credentials. Permission upgrades
delete credentials whose grants differ. Uninstall now deletes every app credential
immediately, so restoring the app cannot revive a pre-uninstall token. This
deliberately strengthens the old Go uninstall behavior.

Private-record transactions recheck and lock the account, installation and exact
credential after HTTP authentication. They require the requested storage grant
and current expiry. A request authenticated before revocation cannot later write
through a stale in-memory session after uninstall/reinstall. Internal token hashes
and account IDs never appear in the app session response.

Seven restricted PostgreSQL HTTP tests cover catalog aliases, permission review,
credential hashing/expiry, account/app separation, active Space membership,
upgrade revocation, recovery/idempotency, stale requests, retained data, purging
denial, event-failure rollback and simultaneous installs. Existing app-runtime
tests also verify transaction-level rejection of mismatched app/account sessions.

## Native private-data purge

`MISTY_NATIVE_APP_PURGE_JOBS=1` enables the native worker after ownership cutover;
it defaults off. The worker claims at most 25 due jobs, locks account and
installation in the same order as install/uninstall, then rechecks the job. Claim,
`purging` state and audit event commit together. Reinstall can cancel a pending
candidate before claim; after claim it receives the existing conflict response.
Expired running claims are reclaimed after fifteen minutes. Attempt number plus
the exact PostgreSQL start timestamp fence completion and failure from older
workers. Failed jobs back off without changing the recovery deadline.

Completion atomically removes only the account's matching app-private records,
app credentials and activity, then marks installation/job complete and writes the
audit event. It preserves other apps/accounts and all Space-owned content. A
failed completion rolls back the entire deletion; a fenced failure transition
can safely restore recoverable state. Errors stored in jobs/events are fixed codes.
A new installation after purge starts with empty private data.

Three additional restricted-role tests cover recovery deadlines, namespace
isolation, preservation of shared notes, reinstall/claim contention, competing
workers, stale leases, audit-failure rollback, backoff and clean reinstallation.

## Remaining ownership gates

Native onboarding now atomically creates the default Space and reviewed default
apps; see `spaces-cutover.md`. Activity tracking and most domain RPC dispatch
beyond Journal, Space reads and Planner task reads remain pending. The Go purge worker remains the sole active
owner until the explicit native switch is enabled as part of a reviewed cutover. Runtime grants and recovery/rollback rehearsals
remain deployment requirements. No catalog promotion, publication or deployment
was performed by this stage.

Journal collaboration also requires a coordinated host transport. Existing Go
note/drawing tickets bind user, Space, resource/type, room, role and ACL version,
and expire after 60 seconds for a single join. Both managed and self-host runtimes
accept `/parties/note-room/{room}` or `/parties/drawing-room/{room}` with the ticket
query parameter and Yjs sync/awareness frames. They enforce viewer/ACL restrictions
and signed room control commands.

Ticket expiry is a join deadline, not a socket lifetime. Tickets and socket state
do not identify an app installation/session; deleting an app credential does not
disconnect a running Yjs connection. Go account deletion has room/user disconnect
outbox commands. Downloaded Journal therefore needs the SDK task's host-owned,
leased transport with opaque handles, bounded frames and teardown on account,
Space and app changes. Native note/drawing handlers, ticket issuance and signed
projections now exist; native-issued tickets pass real managed-worker checks.
See `journal-cutover.md` for evidence and remaining broader lifecycle and self-host requirements. Neither Journal promotion nor full collaboration parity
is proven.
