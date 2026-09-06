# Native Spaces and onboarding

Hono serves account onboarding, Space list/create/read/rename, provider setup
read/update and the public template catalog under bare, `/api` and `/v1` aliases.
Public `spaces.get` and `spaces.members.list` RPC use the native HTTP handlers.
Together with Journal, Planner task reads, connections and Inbox account/folder
reads, native domain RPC coverage is 28
of the current 74 methods. Other methods remain explicit 501 responses.
Member/agent reads, permission changes, invitation
workflows, member removal/leave and ownership transfer are native. Go remains
deployed; complete deletion/account lifecycle and integration migration remain incomplete.

## Atomic creation and retries

Onboarding creates the protected default Space, security domain, owner membership,
zeroed storage accounting, initial everyone role, reviewed default app installs,
Space/install events and completion fingerprint in one transaction. The defaults
remain Inbox, Chat, Journal, Files and Agents in that order. The compatibility
`app_ids` field never selects grants or changes the defaults. App installation
reuses the same transaction helper as ordinary install, including purging-state
protection and credential revocation on grant changes.

An account lock and onboarding/owner advisory locks serialize completion with
ordinary creation. A committed retry returns the existing Space and current
installed apps; changed normalized names or default-app specifications conflict.
An existing active default Space prevents a second onboarding workspace. Failed
completion rolls back the Space, app installs, counters and events together.
Go and native implementations verify the same fingerprint fixtures, including
HTML escaping, Unicode and sorted app specifications. Existing Go retry records
therefore remain readable for the verified contract.

Ordinary creation preserves all six built-in templates, canonical selected
providers and the existing Idempotency-Key fingerprint. Owner creation is
serialized before checking plan allowances; pending-deletion Spaces still count.
The first active Space becomes the protected default. Templates seed task numbers,
ordered collections and note projections; note content enters the durable
bootstrap outbox. The native collaboration sender already verifies bootstrap retry
behavior against the actual managed Worker. No Go process is required to build
or load the template catalog in the native API.

## Authorization and responses

Account-only routes include listing all Spaces, creation, rename, setup changes
and onboarding. An app credential can read only its bound Space with `spaces.read`.
The repository rechecks active Space/account, exact live app credentials and
membership under locks. Owner mutation locks the Space before the account, then
checks current membership. Space owners receive management permissions; members
receive the existing fixed defaults plus explicit per-member allow/deny overrides.
Message/attachment and task dependency rules are applied to returned permissions.
Legacy role JSON is preserved at creation but does not override the current fixed
membership policy.

Member lists return current memberships and visible agent work summaries. Agent
instructions, model and reasoning settings are selected only for their owner;
another member sees only shared identity/activity fields. Disabled agents without
shared history and unrelated private agents are omitted. Permission reads allow
self-inspection or owner inspection; only the owner may change another member's
configurable allow/deny/inherit overrides. Owner permissions cannot be edited.
Updates and their audit records commit together under the Space/member locks.

## Membership changes and ownership transfer

Account-only removal and leave lock the Space before checking the current actor
and member. Owners cannot remove themselves or leave. A successful removal deletes
conversation membership, cancels the member's active agent runs/jobs, denies
pending tool approvals, detaches run contexts, cancels queued/leased device work
and disables that member's Space agent/workflow schedules. Terminal run history,
other members' work and shared documents remain intact. Retrying runs are included
alongside queued/running/waiting states. Permission overrides remain the owner's
policy for that identity if it rejoins; joining does not restore conversations or
resume canceled work. Account/app credentials still undergo current membership
checks; downloaded apps cannot invoke these management endpoints.

Both note and drawing ACL revisions advance and durable room-control commands
commit with removal. This closes the previous Go note-revocation omission without
archiving Space-owned content. Existing sockets close when the collaboration
worker delivers the commands; this is not synchronous network revocation at SQL
commit. New API reads/tickets fail immediately after removal. Authorized document
writes that already hold a Space lock finish before removal, so their newly
committed documents also enter the revocation queue. The member event and existing
`misty_space_control` notification commit in the same transaction. The native
realtime subscriber and production revocation-latency checks remain outstanding.

Ownership transfer protects the default Space, requires a current active member
as recipient and serializes recipient capacity with ordinary creation. Pending
deletion Spaces count toward that capacity. The transaction locks the Space,
then both owners and stale-reservation accounts in sorted order before wallets
and the owner-creation advisory lock. Expired AI reservations release both member
and Space reservation counters. Live AI, upload or rendition reservations block
transfer; a rejected transfer rolls reclamation back as well.

Owner/member roles, Space owner, security-domain owner/version, Space AI allowance
and the ownership event change atomically. Existing consumption survives a lower
allowance and a later transfer back. Personal AI balances are not transferred.
Existing oversized storage remains accessible; the incoming owner's capacity
governs future reservations. This preserves the existing Go capacity policy.

## Deletion requests and existing lifecycle gaps

`DELETE /spaces/:spaceID` preserves the exact-name confirmation, owner-only access,
protected default Space and 30-day pending-deletion deadline. It now cancels all
active Space agent/device work and schedules, queues ACL revocation for both
Journal document kinds, and records the deletion event and control notifications
atomically. Documents, memberships and storage metadata remain available for
future recovery processing; this operation never queues room purge or destroys
stored objects. New native reads, tickets, invitations and reservations reject
the inactive Space. Existing reservation handles can still settle/release through
the usage subsystem, or expire through its established lease logic.

Control notifications page member identities and split payloads by encoded byte
size below PostgreSQL's 8 KiB limit. A 303-member database test verifies that every
member receives a committed notification with no oversized payload. Three further
tests verify lifecycle/cancellation rollback, all-owner work isolation, content
preservation, exact confirmation, default protection and app-token rejection.

Source inspection found no existing Go Space recovery endpoint or permanent
Space deletion worker. `PurgeExpiredSpaceData` cleans messages/invitations/tickets/
events, not expired Spaces. The 30-day field therefore does not prove that Go ever
completed Space deletion. Native recovery and permanent cleanup require an explicit
storage/collaboration/provider-aware implementation, hold checks, accounting and
crash recovery before this lifecycle can be considered production-ready. Do not
replace that missing behavior with cascading SQL deletion.

Space lists include only current memberships, incoming invitations, the native
plan projection and personal contribution/reservation totals across active Spaces.
The read omits expired, consumed, revoked and inactive-Space invitations; unlike
the previous handler, listing does not globally mutate expired invitation rows.
Names retain Go's whitespace and 80-Unicode-code-point boundary. Retry keys retain
the 200-byte bound. Native creation returns the same fully populated Space shape
as a subsequent read, including its permission map.

The template catalog's provider configuration currently preserves the existing
GitHub credential-availability flag. It does not imply native OAuth completion;
provider authorization and connection ownership remain migration gates. No
credential values are exposed in this response.

## Verification and remaining gates

Fifteen foundational restricted-role PostgreSQL HTTP tests cover atomic onboarding, default-app
order/grants, concurrent retry, rollback, creation/onboarding contention, ownership
limits, all six template seeds, canonical idempotency, member permission overrides,
owner-only mutation, current app-session denial, SDK result parsing, invitation
visibility, plan/storage response fields and input bounds, agent privacy/work-state
summaries, permission mutation rollback, and invitation delivery/response races.
See `invitations-cutover.md` for the invitation queue and legacy-link evidence.
Go/native fixture tests
verify onboarding fingerprints and the public template catalog. The general API,
app-installation and Journal regression suites run alongside these tests.

Twelve additional lifecycle tests cover account/app separation, concurrent
leave/removal, shared document preservation, run/approval/context/device cleanup,
rollback, committed PostgreSQL notifications, writes racing removal, default-Space
protection, lower-plan transfer, expired/live reservations, competing transfers,
creation versus transfer and owner/security-domain/wallet rollback, plus deletion
requests and bounded notification delivery.

Still required: default-Space/account deletion/recovery coordination and cleanup,
connected providers, realtime delivery, administrative operations, production role
grants, larger-data performance/lock rehearsals and hosted/self-host deployment
verification. The native invitation worker includes bounded expiry cleanup; its
ownership switch defaults off. Readiness stays `migrationComplete=false`.
