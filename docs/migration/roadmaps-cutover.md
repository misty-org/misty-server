# Native Roadmap CRUD and graph reads

Roadmap list/create/get/update/delete now use the private Planner Roadmap module,
including bare, `/api` and `/v1` aliases and public SDK dispatch. Native HTTP handler
coverage is74/74 after the subsequent integration list/bind port (see `connections-cutover.md`). Application schema remains168. The server now uses the SDK task's
reviewed201-method contracts archive; its74 HTTP routes are unchanged.

The public create handler trims/validates name and description, assigns the
requesting creator, creates a Space-visible roadmap and seeds First milestone at
rank1024 and position80,80. Caller-supplied creator/audience fields do not change
those values. The existing internal conversation-roadmap creation path remains a
Misty/Agent integration obligation under R4; private existing graphs are readable
and editable only by participants in the same Space conversation.

Listing excludes archived/private-inaccessible graphs and retains updated/id
ordering. A graph snapshot includes active milestones, goals and nodes, all active
Space node definitions plus archived definitions referenced by its active nodes,
and only edges whose endpoints are present. It loads all linked task DTOs visible
to the actor; archived/canceled tasks remain in those arrays but do not contribute
to progress. Manual completion applies when a goal has no counted tasks. Goal and
overall percentages retain Go's integer rounding and milestone status rules.
Date-only targets are converted to UTC midnight rather than local host timezone.
Read snapshots use one repeatable-read transaction.

## Authorization and mutations

Account and installed-App requests require active account/Space membership and
the existing tasks.view/manage permissions. Apps additionally need roadmaps.read
or roadmaps.write; write-only Apps can create/update without roadmaps.read.
Private roadmap access requires a human conversation participant and matching
Space. The native graph loader also checks each linked task's audience and Space.
The Go loader did not perform that per-task check; native results intentionally
omit inaccessible task payloads and compute their progress from visible tasks.

Updates and archives lock the visible graph and require its current positive
expected_version. A stale graph returns409; a missing/archived/inaccessible graph
returns404. Each mutation increments the graph version and commits its existing
Space event/notification in the same transaction. Archive preserves its children
and prevents normal listing/reading/editing. Repeated archive returns404, matching
Go. Read-only body fields cannot alter creator or audience.

## Evidence and remaining work

Four new PostgreSQL HTTP/RPC cases pass with public response parsing: the five
methods and seed graph, a complete graph with archived/custom/private linked data
and progress, App/private permission boundaries, and validation/concurrent writes/
event rollback. The new restricted-role fixture includes the existing owner-storage
trigger privileges.21 existing task/Calendar cases and21 affected RPC/authorization
unit cases also pass. Typecheck/build pass.

SDK contracts were synchronized from the reviewed archive with SHA256
`96a5eccadd380a5a8bd1d567d270297f413bb29ae5620af33bd927b8d3d93ba0`.
The added native-only files.replaceCopy and files.openExternal do not change HTTP
routing, and Files catalog scopes were left unchanged at the SDK task's request.
No new migration, full-suite/image rerun, provider call or deployment occurred.

## Milestone, goal and task-link mutations

Seven additional methods now execute in Hono: milestone and goal create/update/
archive, plus goal task-link replacement. They share the graph mutation transaction,
permissions and optimistic expected_version. Failed child validation or event
recording rolls back the graph increment and all child changes.

Milestone creation appends rank in1024 steps, defaults dimensions to440×360 and
uses requested coordinates. Goal creation appends within its milestone and retains
Go's zero coordinates when omitted. Normal milestone/goal PATCH changes text,
date and optional positive rank; layout fields stay reserved for the later layout
route. Mutation responses retain Go's uncomputed progress fields; graph reads
calculate the current progress. Target timestamps preserve their input calendar
date when stored in the existing DATE column.

Archiving a milestone also archives active child goals and nodes and deletes
edges touching the milestone or its nodes, matching Go. Goal archival keeps its
links and filters the goal from subsequent graph reads. Task records survive both
operations. Replacing task links trims/sorts IDs, rejects duplicates or more than100,
requires active visible tasks in the same Space, replaces links atomically and
clears manual completion for any nonempty replacement. Empty replacements retain
the existing completion metadata, matching Go.

Manual completion requires no active, noncanceled linked tasks. The transaction
locks linked tasks before updating the goal, matching the existing task-trigger
lock order and preventing a task from reopening between validation and completion.
The server sets completion time/user from the authenticated actor. It ignores
caller-supplied manual completion metadata; omitting complete_manually preserves
the stored metadata. This intentionally differs from Go's trusted full-goal body,
which could attribute completion to another user or clear it on an unrelated edit.
Explicit false clears completion. The existing task-status trigger still clears
completion when a linked task reopens.

All7 Roadmap database tests pass, including3 new cases covering all7 SDK methods,
public response schemas, defaults, progress/task links, manual completion,
private/cross-graph rejection, stale versions and atomic archive rollback.
Typecheck/build pass. The built dispatcher sets confirm61 of74 public HTTP methods
have native owners. The remaining13 are11 node/definition/edge/layout methods and
integrations.list/bind; the broader Go REST/job/admin inventory remains open.
No schema/public contract change, whole-suite/image rerun, deployment or external
provider operation occurred at this checkpoint.

Internal Agent
creation and complete realtime/background delivery remain tracked. No entire
R1–R12 requirement closes here. Go remains deployed and native readiness stays
false pending the full replacement and assembly.

## Custom node definitions and node mutations

The four definition methods and three node mutation methods are native, including
all REST aliases and the installed-App dispatcher. Definition metadata is trimmed;
field schemas retain Go's validated original identifiers/options and enforce the
existing20-field, type, label, option and payload bounds. Existing field IDs must
remain and cannot change type. Metadata edits and archive use definition versions,
without incrementing roadmap graph versions. Definition events commit atomically.

Node writes validate custom values, same-Space definition and same-graph active
milestone references. Kind and definition cannot change on update. Definition
archive prevents new custom nodes but permits editing existing nodes; graph reads
retain referenced archived schemas. Coordinates/date handling and graph version
checks match existing mutations. Node archival removes incident edges. Definition
rows are locked during validation so concurrent schema/archive writes cannot race
node admission. JSONnull/non-array schemas and non-object values return400 before
the existing database shape constraints rather than generating a database error.

All10 Roadmap PostgreSQL cases and typecheck/build pass. Three added flow tests
exercise the seven SDK methods, result parsing, archived-definition lifecycle,
typed fields and schema evolution, current scopes, cross-Space/graph references,
version conflicts and event rollback. Built dispatch confirms68/74 methods. The
remaining6 are edge create/update/delete, layout update and integration list/bind.
No schema/contracts change, full-suite/image rerun or deployment occurred.

## Edges and layout

Edge create/update/delete and layout update complete all public Roadmap methods.
Edge writes retain legacy source_goal_id/target_goal_id fallback, normalize
legacy dependency to depends_on, validate active same-graph endpoints and the
existing risk/decision/metric/note relationship rules, and reject causal cycles
between goals. Creation and updates both emit roadmap.edge.updated; deletion
emits roadmap.edge.removed, matching Go. Graph locking serializes validation and
writes. Public IDs and ownership come from server context, not body identity.

Layout accepts at most1000 combined items and existing coordinate/dimension
bounds. Milestones resize/move; goals move only to active milestones in the same
graph; nodes attach to a milestone or detach when milestone_id is empty. Text,
rank and unrelated fields are ignored. Child versions and one graph increment
commit with roadmap.layout.updated; failed items or events roll back all changes.

All12 Roadmap PostgreSQL cases pass, with typecheck/build. Two added flows exercise
all4 public SDK methods/result parsing, legacy endpoint conversion, relationship
rules/cycles, layout coordinates/parent changes, stale versions, scopes and atomic
rollback. Built registry coverage is72/74; integrations.list/bind remain. The
SDK201 archive was synchronized and independently hash checked; files.listArchive
is desktop-only and does not change the74 HTTP methods. No whole-suite/image run,
schema change or deployment occurred. Internal Agent/realtime/background assembly
and the broader migration checklist remain open.
