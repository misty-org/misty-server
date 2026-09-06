# Implementation completion checkpoint — 2026-09-06

The agreed goal remains a complete Go server replacement, not a Hono scaffold or
a subset launch. Go is still deployed. Native dispatch currently implements
all 74 public HTTP RPC methods; the SDK also has native desktop methods
that are not HTTP endpoints. No percentage or ETA is inferred from those counts.

Runtime organization is locally complete: agent runtime, managed Yjs collaboration
and self-host collaboration have independent application folders and build paths.
Private API/payments boundaries, contract packaging and many domain foundations
are implemented. Their remaining integration and release obligations are below.

Priority update from the user: finish the local replacement and make Go retirement
achievable. Port existing behavior and reuse working contracts/logic. Verification
is build/typecheck, affected tests and representative flow checks, followed by one
bounded integration pass after assembly. Fix observed failures and concrete
permission, integrity and lifecycle blockers. Do not expand each port into a new
fault matrix or unrelated legacy repair project. No live cutover is authorized.

The table tracks implementation coverage and concrete dependencies. Previously
recorded verification remains evidence, not a reason to repeat unchanged suites.

| ID | Required implementation | Completion check | Concrete dependency / blocker |
| --- | --- | --- | --- |
| R1 | Route, job and command coverage | Reconcile effective Go mounts/aliases, startup jobs and admin commands against native owners; no silent omissions. | Current registration comparison:519 baseline aliases have main registrations,8 are factory-only,705 are absent (241 canonical operations). Conditional routes and12 worker roots are recorded. Refresh and resolve owners as ports complete. |
| R2 | Account and Space deletion | Connect initiation, billing/provider cleanup, retention, private-data purge and finalization; preserve shared attribution/accounting and queued object cleanup. | Final purge, remaining provider/Space workers and ownership handover are incomplete. |
| R3 | Planner | Implement task writes/move/batches, Calendar sources/events/sync, roadmap and automation routes/jobs using existing behavior/contracts. | Task methods/effect fan-out, Calendar, all public Roadmap graph mutations are native; task batches and automation integration remain. |
| R4 | Misty, agents and tools | Assemble conversations/streams, attachments, model calls, definitions/grants/memory, approvals/context/tools, scheduling, callbacks and completion. | Signed callback factory is implemented; public admission, dispatch, usage adapter, context/tools/completion and Space-agent runtime path remain. |
| R5 | Library and transfers | Port remaining files/assets/search/indexing, processing, import/export and recovery handlers/jobs. | Journal/storage foundations, core Library reads/downloads/mutations and album/folder/group organization are composed; asset stacks/edit versions and remaining transfer/processing jobs are open. |
| R6 | Integrations and realtime | Complete remaining OAuth/provider lifecycle, subscriptions/webhooks, mail reconciliation and realtime handlers/jobs. | Mail's eight methods and OAuth are composed; remaining provider/background flows need owners. |
| R7 | Payments and usage | Connect isolated payments to all existing billing flows and metered callers, complete legacy handover and closure/reconciliation commands. | Private service and ledger exist; metered Misty callers and final handover remain. SDK contains no billing. |
| R8 | Other API/account/admin paths | Port every remaining R1 account/session/device/self-host/admin entry using existing IDs, data and permissions. | Resolve the coverage inventory rather than add new features. |
| R9 | SDK/client integration | Wire all 74 HTTP RPC methods and supported REST flows to native owners; use the reviewed public contracts and fix actual client failures. | All74 named HTTP methods have native handlers; supported non-SDK REST clients and complete background/runtime integration remain. SDK Apps may still use working Go paths. |
| R10 | Local assembly and operations | Wire API/payments/runtime startup, workers, manifests, roles, secrets, migrations, health/shutdown and admin commands for hosted/self-host local installation. | Complete missing domains and ownership handover, including existing Go-owned Calendar sources, before enabling final assembly. |
| R11 | Bounded assembled verification | Build/typecheck, affected tests and representative integrated flows pass, including permissions, persistence/restart and a viable upgrade/rollback. | R1–R10. Report unavailable external/platform checks separately; fix observed failures without expanding into exhaustive qualification. |
| R12 | Retire local Go server dependencies | Local deployment/startup, CLI/admin, CI and builds use the native replacements; fresh installation and rollback work. Historical SQL/parity fixtures may remain. | R1–R11. No production deployment, live Go shutdown or destructive live-data changes. |

## Optional follow-up hardening

Broad production qualification, exhaustive fault matrices, speculative edge-case
exploration, additional benchmarks, unrelated legacy fixes, cosmetic refactors,
new abstractions/providers/features and optional dashboards are follow-up work.
Existing features, basic permissions, data integrity, required lifecycle behavior
and a viable rollback remain required. Live deployment is a separate user action.

## Completed bounded deliverable at this checkpoint

Migration 163 preserves original/model AI attachment object keys in durable jobs
when attachment metadata is deleted, soft-deleted or replaced, including
conversation/account cascades. The native storage worker protects keys still
referenced by live attachments. This is required by R2: otherwise deleting a
conversation loses the only cleanup keys and can strand private images.

Verification: five new restricted-role cases plus existing avatar/Journal cases
(35 tests total), three migration-safety/history cases, strict TypeScript and
build. Tests cover rollback, ownership after account cascade, duplicate keys,
later deadlines, live cross-variant references and failed-delete retry. No live
storage deletion or deployment occurred. The existing AI upload URL lasts 15
minutes; the cleanup delay is 30 minutes. Native attachment issuance and old
writer drain must preserve that bound during R4/R10.

The last complete suite before this fix was 350 database tests and 204 unit tests
through API migration 162, with six image modes verified. Targeted results above
are not presented as a new full-suite or refreshed-image run.

## Completed: private AI/agent retention phase

The R2 private AI/agent transaction is implemented. It removes private
conversations/events, invocation payloads/artifacts, retrieval chunks and memory;
redacts owned agent definitions/versions and private run payloads; preserves shared
results, attribution, IDs and license records; and retains attachment cleanup
intents. Pending-account, native ownership, retention, prerequisite and lease
fences are enforced. Related work must stop before erasure. Only the phase receipt
is recorded; account deletion remains scheduled. See `account-deletion-agent-purge.md`.

Verification: seven targeted restricted-role PostgreSQL cases passed, including
real lease expiration during SQL and complete rollback. Strict TypeScript/build
also pass. No migration, public contract, deployment or image change was made in
this deliverable. No whole R1–R12 requirement is closed by this phase alone.

## Completed: private account settings/history phase

The explicit account-state phase and shared retention/lease transaction are
implemented. The ownership snapshot classifies 183 direct/composite user
references; 76 remain assigned to other required policies/workers. This snapshot
is not a complete transitive payload inventory or permission for blanket deletion.
Provider/MCP, App, device, Library, owned-Space and remote-object obligations remain.
See `account-deletion-private-state.md` and `account-data-ownership.json`.

Verification: five new account-state database cases plus seven AI/agent phase
regressions passed, including actual SQL lease expiry and Space contention.
Typecheck/build passed. No full-suite rerun, schema/public contract change,
image refresh or deployment occurred. R2 and R1–R12 remain open as previously listed.

## Completed: AI cancellation and late-callback safeguard

Account disable now cancels active AI invocation state, disables AI/recap/proactive
settings and detaches contexts while preserving unresolved external effects and
recap lease evidence. The native callback persistence repository enforces active
account/settings and exact runtime identity; migration 164 preserves private
resulting-state receipts for exact replay and conflict detection. It remains
unmounted pending signed HTTP/runtime composition. See `misty-lifecycle.md`.

Verification: 43 targeted database cases passed across deletion, both purge
phases, local cleanup, callbacks and migration safety; typecheck/build passed.
Coverage was broadened to those modules because the shared affected-Space query
and event schema changed. No SDK/public contract change, deployment or new full
release claim was made. No whole R1–R12 requirement is closed by this safeguard.

## Completed: native invocation creation and activation repositories

Account-session creation and runtime activation repositories now enforce exact
session, account/AI settings, Space membership, private conversation binding,
idempotency and runtime identity. Callbacks share the membership/conversation
checks. Real expiry during creation/event SQL rolls back writes. This remains
existing R4/R6 admission work; HTTP, provider dispatch and metering remain open.
See `misty-admission.md`, including the intentional conflict response for a
different body under an existing idempotency key.

Verification: eight new admission tests plus four callback regressions passed,
with typecheck/build. Review found the deleted-conversation authorization gap;
that was fixed and specifically tested within this deliverable. No schema/public
contract change, deployment or broader release-readiness claim was made.

## Completed: signed Misty runtime activation and node-event ingress

The Hono callback factory accepts the existing workflow HMAC protocol, activates
native-owned invocations and persists the existing stream DTOs for tool/model
progress. Node completion does not terminate an invocation. Signed node/state
fields determine replay identity; changing the unsigned idempotency header cannot
duplicate stream events. Model-start admission calls an explicit usage dependency
outside callback SQL, then revalidates access before committing the receipt.

Migration 165 records runtime ownership (existing rows default to Go) and private
callback hashes. Native creation assigns Hono ownership. Legacy invocations are
not silently adopted. The factory is registered as an optional API dependency;
production main does not enable it until usage/dispatch/context/completion are
assembled. This completes the selected ingress implementation, not a whole agent
run. See `misty-runtime-callbacks.md`.

Verification: two focused HTTP tests and fifteen PostgreSQL tests across runtime
routes, admission and invocation persistence passed. Typecheck/build passed. No
full-suite/image rerun, SDK contract change, live provider call or deployment.

## Completed: native task create/update/archive/move

Four additional task methods and REST aliases now run in Hono, bringing native
HTTP handler coverage to 39/74. They preserve existing normalization, numbering,
last-write-wins updates, idempotent tombstones, rank rebalance, source references,
Agent assignment/cancellation and activity/events. Migration 166 atomically keeps
pending automation/assignment effects for the native worker; that worker is still
required and is not represented as complete. See `planner-cutover.md`.

The SDK task requested its reviewed Inbox1.1.0/permission3 promotion be reflected
in server install/session grants. Only Inbox differed in the source catalog;
both generated server catalogs were synchronized. Exact grants and rejection of
the old permission version were verified. SDK198/74 contracts remain unchanged.

Verification: ten Planner and ten official-app PostgreSQL tests passed, along
with catalog parity, typecheck/build. This includes five new mutation flow cases.
No full-suite/image rerun, deployment, real Agent execution or mailbox operation.
No whole R1–R12 obligation is closed by this handler checkpoint.

## Completed: task effect consumer and native work ownership

Task effects now produce assigned-Agent runs/jobs/owned device contexts and
consented task-change workflow claims with durable dispatch inputs. The source
effect completes atomically with that downstream database work. A workflow claim
remains pending execution; no full Agent/workflow execution is claimed. Startup
can enable the worker explicitly with `MISTY_NATIVE_TASK_EFFECTS_JOBS=1`.

Migration 167 records native run/claim ownership. Go job claims, terminal-job
synchronization and stale-run reconciliation exclude native runs. Existing live
binaries must still be drained or updated before mixed ownership. See
`task-effects.md` for remaining runtime/capability/usage dependencies.

Verification: five new effect cases and ten affected Planner cases pass; two
focused Go scheduler checks pass on the separate test database, including native
ownership exclusion. Typecheck/build pass. The fixture setup was corrected for
existing device/workflow constraints and cleanup ordering. No repeated whole
suite/image test, SDK contract change, worker deployment or external operation.

## Completed: Calendar events and combined agenda

Five additional methods now use Hono: Calendar event list/create/update/delete
and agenda.list, including their REST aliases. Native HTTP handler coverage is
44/74. Event edits/archive preserve optimistic versions; task mutations retain
their separate last-write-wins behavior. Agenda combines tasks, imported/native
events and roadmap dates with existing visibility and exclusion rules. Mutations
record their Space event atomically. See `planner-cutover.md`.

Verification: fourteen Planner PostgreSQL HTTP/RPC cases pass, including four new
Calendar/agenda cases and public response parsing. Typecheck/build pass. Test
fixtures were corrected for the existing unified Google provider name and random
ID ordering among equal-time events. No schema/public contract change, whole-suite
rerun, image refresh, provider call or deployment occurred. No whole R1–R12
obligation is closed by this checkpoint.

## Completed: Calendar source management and explicit Google synchronization

Five more methods use Hono: calendar.sources.list/create/delete,
calendar.google.calendars and calendar.sync. Native HTTP coverage is 49/74.
Legacy Space integration credentials are read/refreshed in their existing encrypted
format; they are not replaced with personal mail connections. Provider pages are
fetched outside SQL and commit only after current actor/credential/source checks.
Incremental imports preserve pending native workflow claims. See
`calendar-sources.md` for behavior and remaining watch/background obligations.

Verification: eighteen Planner database tests pass, including four new source
HTTP/RPC flows, cursor expiration/rebuild, cancellation, one encrypted credential
refresh, workflow replay and disable-during-import. Six affected credential unit
tests and typecheck/build pass. Restricted-role fixture grants were corrected
for the row locks on workflow instances. No schema or SDK contract change, full
suite/image rerun, live Google call or deployment. No R1–R12 item closes here.

## Completed: Calendar watches, callbacks and periodic reconciliation

Native Calendar synchronization now registers/renews Google watches, accepts
existing callback aliases and records durable refresh generations. A leased
worker reuses the import path, preserves callbacks arriving during a run and
retries failed work. Startup can enable it with MISTY_NATIVE_CALENDAR_JOBS=1;
shutdown aborts in-flight provider requests before draining the worker.

Migration168 records source ownership and durable generation/lease state. Existing
sources default to Go; newly created native sources belong to Hono. Go scheduler
selection and callback lookup exclude native sources. Native scheduling/import
claims exclude Go sources; re-publication cannot silently transfer ownership.
Explicit legacy handover and draining/updating old Go binaries remain R10 work.
See `calendar-sources.md` for configuration, behavior and limitations.

Verification:21 Planner PostgreSQL cases pass, including3 new background cases;
2 migration-safety cases and1 focused Go scheduler/callback exclusion case pass.
Typecheck/build pass. The Go test uses its separate disposable database. No public
contract/count change (49/74), whole-suite/image rerun, live Google request,
worker deployment or Go shutdown. No whole R1–R12 obligation closes here.

## Completed: Roadmap CRUD and graph snapshots

Five more methods now execute in Hono: Roadmap list/create/get/update/delete.
Creation seeds the existing first milestone; graph reads include goals/tasks,
milestones, nodes, definitions, visible-endpoint edges and progress. Updates and
archive enforce optimistic graph versions and commit their Space events atomically.
Native linked-task reads apply the existing private task audience check, closing
the concrete disclosure found in Go's graph loader. See `roadmaps-cutover.md`.

The SDK task supplied its reviewed200-method contracts archive. Its SHA256 and
snapshot integrity were verified, the private server copy was synchronized, and
Go's74 HTTP routes remain unchanged. The additions files.replaceCopy and
files.openExternal are native-only. No Files catalog scope promotion was made.

Verification:25 affected Planner PostgreSQL cases pass (4 new Roadmap cases plus
21 existing task/Calendar cases),21 RPC/authorization unit cases and typecheck/build
pass. New fixture grants were corrected for the existing owner-storage trigger;
its owner-permission assertion was corrected to test a member, since owners retain
all permissions. Public response parsing and concurrent version conflict checks
pass. Schema remains168, native HTTP coverage54/74. No whole-suite/image rerun,
provider call, deployment or whole R1–R12 closure.

## Completed: milestone, goal and task-link mutations

Seven additional SDK methods now use Hono. Milestone and goal create/update/archive
and goal task-link replacement share current permissions, graph locking/version
checks and atomic Space events. Milestone archival updates its child goals/nodes
and removes affected edges while retaining task records. Task linking requires
visible current tasks; manual completion uses the acting user and current linked
task state. See `roadmaps-cutover.md` for preserved behavior and the explicit
server-authoritative completion metadata change.

Verification:7 Roadmap PostgreSQL HTTP/RPC cases pass (3 new mutation flows plus
4 existing CRUD/graph cases), with typecheck/build. Public response schemas,
all7 new methods, cross-graph/private link rejection, stale versions and archive
rollback are exercised. Built dispatcher sets were compared with the public HTTP
registry:61/74 native, with exactly11 Roadmap node/definition/edge/layout methods
and integrations.list/bind remaining. This is HTTP handler coverage, not the full
Go route/job/admin inventory. Schema168 and SDK200/74 stay unchanged. No whole
suite/image rerun, provider call, deployment or entire R1–R12 closure.

## Completed: custom node definitions and node mutations

Seven more public methods and REST aliases now execute in Hono. Definition
list/create/update/archive preserves field IDs/types across edits and uses its
own optimistic version. Node create/update/archive validates typed custom values,
current graph/Space references and immutable kind/definition identity. Archiving a
definition excludes new nodes but retains existing editable nodes and their schema
in graph reads. Node archival removes incident edges. Events and graph increments
commit atomically. Invalid JSON schema/value shapes return400 before database
constraints instead of exposing a database failure.

Verification:10 Roadmap PostgreSQL cases pass, including3 new custom-node flows,
all7 methods and public result parsing, archived-definition editing, validation,
permissions, cross-Space/graph references and event rollback. Typecheck/build pass.
A fixture assumption about an existing second Space was corrected. Built dispatch
sets confirm68/74 native HTTP methods: only4 edge/layout methods and2 integration
methods remain. Schema168 and SDK200/74 are unchanged. No full suite/image rerun,
provider call, deployment or whole R1–R12 closure occurred.

## Completed: Roadmap edges and layout

All four remaining Roadmap SDK methods and REST aliases execute in Hono. Edge
mutations retain legacy goal endpoint inputs, normalize dependency to depends_on,
validate active same-graph endpoint kinds and reject causal goal cycles. Layout
updates move/resize milestones, move goals between active milestones and attach or
detach nodes using existing bounds. Graph versions, child changes and events are
atomic; an invalid later item cannot leave earlier items moved.

Verification:12 Roadmap PostgreSQL cases and typecheck/build pass. Two additional
flows exercise all4 SDK methods and result schemas, endpoint normalization, node
edge rules, cycle rejection, layout moves, failed-layout rollback and App scopes.
Built dispatch confirms72/74 HTTP methods; only integrations.list/bind remain.
Reviewed SDK201 contracts were synced from the exact archive with SHA256
`e874552ee0df35e4301135b1f5f46be736a195b163975180ad1e5fda34901fbf`.
The added files.listArchive method is desktop-only; HTTP routes remain74. No Files
catalog/scopes, schema168, deployment or Go shutdown changes. No R1–R12 item closes.

## Completed: integration listing and connected-account Calendar binding

The final two named HTTP SDK methods now execute in Hono and all three REST alias
families. Listing selects public integration metadata only. Binding requires the
current Space, integrations.manage, an owned active Google account and the exact
Calendar capability. It re-encrypts the token envelope under the existing Space
AAD, preserves expiry/scopes and upserts the existing integration identity. Source
account locking and one SQL transaction prevent partial copies. Rebinding keeps
the stored credential reference aligned with the retained credential row ID.

Verification:25 connection PostgreSQL cases pass, including3 new binding/listing,
permission/capability and SQL rollback cases; the corrected public RPC result
assertion also passes in a focused rerun. Typecheck/build pass. Built dispatch
sets confirm74/74 public HTTP methods with no missing registry entries. This is
not full Go route/job/admin replacement: R1–R12 remain open. SDK201/schema168 are
unchanged. No live Google request, full-suite/image rerun, deployment or Go shutdown.

## Completed: current route/job/admin registration checkpoint

The existing Go route golden passes TestRouteInventory unchanged. Refreshed static
inventory records532 implementation files,232 tests,168 migrations,644 source
registration candidates and12 startup worker roots. A new registration inventory
constructs the Hono factories without service calls, requests, sockets or database
access, reads main.ts dependency composition and compares all1232 Go baseline
verb/path aliases with actual Hono registrations. Run npm run migration:coverage
after changes; it builds before reading the compiled factories.

The baseline currently has438 matching main registrations,8 routes whose factories
exist but are absent from main, and786 absent aliases representing268 canonical
operations. These are registration findings, not behavior/availability guarantees.
Account deletion and runtime callbacks are the two absent main dependencies.
All117 Planner and75 Journal baseline aliases are registered, but their broader
workflow/realtime/lifecycle integration obligations remain open.

The report also lists19 conditional device operations, protected metrics,
Activepieces proxy paths, configured Stripe routing, hosted/self-host distinctions,
all12 worker roots and their direct service calls,9 native polling workers, other
startup obligations and the four Go command entrypoints. Go command retirement
still needs the collaboration-ticket and Smart Library evaluation CLIs; the latter
has not been run against a paid provider. See native-coverage.json and
registration-checkpoint.md. Script execution and the focused Go route test pass.
No service/deployment state was changed and no whole R1–R12 obligation closes.

## Completed: core Library reads and sensitive collection access

Five account-authenticated operations now run in main and all three REST alias
families: item listing, detail, facets, usage and password reauthentication.
Queries retain existing structured search, filters, cursor sorting, cover-only
stacks and DTOs. Storage reuses the current ledgers and owner/member response
split. Grants are password-backed, hashed, scoped to account/Space/purpose,
short-lived and audited. Existing schema168 is sufficient.

Native reads close concrete access gaps in Go: effective structured hidden
filters and visibility=all require reauthentication, and private conversation
items cannot contribute to other members' lists, details or facet counts.
Album counts exclude inaccessible, hidden and trashed items. These deliberate
permission corrections are documented in library-cutover.md.

Four restricted-role PostgreSQL scenarios pass (listing/search/pagination/DTOs;
audience/facets/permissions; reauthentication; storage usage), plus typecheck and
build. Coverage now reports453 main aliases,8 factory-only aliases and771 absent
aliases representing263 canonical operations. All74 public HTTP SDK methods
remain native. SDK201, Files catalog/scopes, database schema and deployment state
are unchanged. No whole R1–R12 obligation closes; Go remains the deployed owner.

## Completed: Library original/current downloads and SDK202 sync

The download operation now resolves original or ready current renditions under
account, Space, audience, sensitive-item and download permissions, and records
views transactionally. S3/R2 retains signed descriptors and the explicit
X-Misty-Signed-Download marker; filesystem storage streams existing Go-format
objects with safe names, private/no-store headers and metadata checks. Streaming
bounds buffers and open files, supports cancellation, and verifies the last chunk
before allowing Content-Length completion. Existing R2 URL lifetime configuration
is reused. No new transfer mode switch is introduced.

The native egress guard retains existing process-local rolling-day environment
limits, now including the proposed transfer before approval and refusing new
identities when accounting is saturated. These prevent the old one-file overshoot
and per-account saturation bypass. Other download families still need this shared
accounting during their assembly; this is not a deployment-global durable meter.

Verification: seven Library PostgreSQL cases pass, including signed/local paths,
original/current rendition selection and processing fallback, permissions, views,
quota, safe headers and object mismatch/absence. Twelve storage/egress unit cases
pass, including20 MB bounded streaming, repeated cancellation, corruption and
symlink rejection. Typecheck/build and coverage pass. A time-sensitive signed URL
assertion was made deterministic by injecting the same clock into signing and
service; no production TTL behavior was changed.

SDK202 reviewed contracts are synced from archive
/tmp/misty-sdk-reviewed-202-file-trash/misty-contracts-0.1.0-30ed3f5677e0da3b.tgz;
SHA25630ed3f5677e0da3bda93e1e66284f4d909e634c683ee1eb06b864245bf0de703,
snapshot check and74 Go route checks pass. files.openTrash is device-only. No
Files catalog/scopes, schema168, provider, deployment or Go shutdown changes.
Current registration totals:456 main aliases,8 factory-only,768 absent aliases
representing262 canonical operations. No whole R1–R12 item closes.

## Completed: Library metadata, bulk actions, trash and restore

Four more operations now run in main across bare/api/v1 aliases. Item updates
retain Go's metadata replacement defaults, tag normalization and optimistic version
checks. All14 existing bulk actions are implemented: flags, tags, date/location,
album membership and trash/restore. Responses preserve requested item order;
album membership changes bump album versions rather than item versions.

Permission, audience, sensitive grants, current versions/states and recovery
eligibility are checked inside the mutation transaction. Rows lock in stable ID
order. Item and contribution changes, audit records and response reads share that
transaction. Trash grants30 days of recovery and changes active contributions to
recovery; restore moves them back to active. Both states remain billable, and no
objects or data are purged. Any failed item or audit rolls the whole bulk action
back. Existing schema168 and account-only Library authorization are unchanged.

Verification: eleven restricted-role Library PostgreSQL cases pass, including four
new scenarios covering metadata normalization/defaults/conflicts, all14 bulk
actions, permission/private-item denial, restore expiry, storage accounting and
actual SQL rollback on audit failure. Typecheck/build and refreshed coverage pass.
The initial typecheck caught a missing helper closing brace, corrected before the
passing run. Current registration:468 main aliases,8 factory-only,756 absent
aliases representing258 canonical operations. SDK202/HTTP74 are unchanged; no
Files promotion, provider calls, deployment or Go shutdown. R1–R12 remain open.

## Completed: album, folder and rule-based group organization

Seventeen organization operations now execute in Hono and all51 REST aliases:
album CRUD/settings, membership listing/add/remove and ordering, folder CRUD,
and group listing/create/matching items. Existing DTOs, names, versions, sort
modes, maximum500 albums/100 groups/12 rules, membership limits, audits and folder
cascade semantics are retained. Deleting folders cascades to descendant folders,
unassigns their albums and leaves albums and Library items intact. Group rules
retain the existing fields/operators and parameterized matching behavior.

Counts, cover IDs and membership writes now enforce item audience as well as
hidden/ready state, closing the old private-item metadata/write gaps. Album item
and group reads retain their audience filters. Native organization writes use a
Space-scoped advisory lock to serialize folder ancestry and count-limit changes;
concurrent opposite folder moves cannot form a cycle. Existing bulk album writes
keep their own item/album row-lock protocol. Shared-library DTOs are read within
the same transaction as successful writes.

Verification: all16 restricted-role Library PostgreSQL tests pass. Five new
organization scenarios cover all17 operations, custom/date ordering, covers,
folder ancestry/cascades/concurrent moves, private-item filtering, version/unique
conflicts, cross-Space folders, edit permissions, album limit, audit rollback and
group rules. Typecheck/build and coverage pass. Current registration:519 main
aliases,8 factory-only,705 absent aliases representing241 canonical operations.
SDK202/HTTP74/schema168 are unchanged. No Files promotion, external provider,
deployment or Go shutdown; all R1–R12 remain open.

## Next bounded deliverable

Port Library asset stacks and edit-version metadata: stack listing/create/update/
delete and edit-history listing/create/current selection/delete. Preserve member
and MIME constraints, item audiences, sensitive grants, optimistic versions,
audit records and rendition cleanup/accounting. Compose all aliases and verify
representative stack/edit-history flows and permission/version failures, then
run typecheck/build and refresh coverage. Rendering/preview workers, uploads,
import/export, indexing and retention remain required later implementation.
