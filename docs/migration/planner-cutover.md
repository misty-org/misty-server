# Native Planner migration

The private Planner module now serves six task methods, four Calendar event
methods and agenda reads under bare, `/api` and `/v1` aliases. All eleven Planner
RPC methods use the shared native dispatcher. Five additional source/Google methods
are now implemented; see `calendar-sources.md`. Five Roadmap CRUD/graph methods
also use native repositories; see `roadmaps-cutover.md`. Seven milestone/goal/task-link methods are also native. Seven node/definition methods are native as well. Edges and layout are native too. HTTP coverage is now74/74 after integration list/bind;
this counts handlers, not complete background processing or the whole migration.
Query parameters retain typed values and URL encoding. No Go fallback is used.

## Task reads and authorization

Task queries preserve the existing 100-row default and 200-row limit, raw base64url
offset cursor (up to offset 1,000,000), status/priority/assignee/search/due filters,
rank/due/updated ordering, optional archived rows and visible status totals. Totals
ignore the page's filters and always exclude archived tasks, matching Go. Cursor
decoding is explicitly bounded and rejects padded/non-URL-base64 inputs. Date
inputs require RFC3339 seconds and an offset; PostgreSQL compares their original
precision so sub-millisecond bounds are not collapsed by JavaScript Date parsing.

Both account and app reads lock the active Space/account and current membership.
App reads additionally require the exact live installation/session and tasks.read;
all actors need the current tasks.view permission. Denying tasks.view also denies
task mutation through the existing permission dependency policy. Conversation
tasks are visible only to human participants in that conversation and Space.
Activity reads use the same audience check and retain archived task history.
Optional empty actor IDs are omitted and integer counters are checked for safe
client precision. SQL columns are explicit; runtime/provider secrets are not
selected.

Five restricted PostgreSQL HTTP/RPC tests cover filters, pagination, aliases,
SDK response parsing, typed query forwarding, private task/activity denial,
permission and credential revocation, archived activity ordering, invalid bounds
and precise timestamp filtering. These prove the read paths, not all of Planner.

## Task mutations

The write repository ports Go's task normalization, numbering, MST keys, priority,
timezone, source references, assignments and completion timestamps. Account/App
writes require tasks.manage, and Apps additionally require tasks.write on their
live installed session/Space. A write-only App does not also need tasks.read.
Read-only fields in full-task PATCH bodies cannot impersonate creators or change
resource audience. Targets and returned reorder lists retain the native reader's
conversation visibility boundary.

Active task changes are last-write-wins, matching Go: a positive client version is
required but does not become an optimistic-lock predicate. The server increments
the stored version. DELETE archives a tombstone; repeat deletion returns it
without another version/event, and update/move cannot resurrect it. Counter and
rank allocation are transactional. Moves reuse midpoint ranks and rebalance a
crowded destination column to 1024-unit spacing. Advisory lock names match Go;
native writes acquire all destination-column locks in stable order because an
Agent assignment can change todo to in_progress.

Source references preserve the existing Library/task/chat attachment permission
and ownership checks. A personal Agent must belong to the acting member and be
enabled. New Agent assignments produce the existing task activity record and
move todo to in_progress. Replacing an Agent cancels its active source-task runs,
leased jobs, pending approvals and attached contexts using the existing SQL logic.
Existing roadmap completion triggers also execute on native task changes.

Every mutation records its existing Space event and notification in the same
transaction. Migration 166 adds `native_task_effects`, retaining the task snapshot
and private Agent context for the remaining native automation/assignment consumer.
Private context does not enter the public Space-event payload. The queue stores
one intent per task/version/event kind; a failed intent insert rolls back the task
and its counter/event. It is not consumed by Go. The native effect consumer is now implemented and
optionally wired into startup; see `task-effects.md`. Assigning an Agent still
does not prove execution by the remaining native runtime dispatcher.

Ten Planner database tests pass (five new mutation cases plus five read cases).
They exercise all four writes through actual SDK RPC with public response parsing,
REST aliases, stale versions/tombstones, rank rebalance, private audience and
scope/permission denial, assignment/cancellation, concurrent counters and atomic
effect recording. Existing trigger and FOR SHARE grants were corrected in the
restricted-role test setup after the first affected run exposed them. Production
role provisioning already grants the relevant table privileges.

The same checkpoint synchronized the SDK task's reviewed Inbox1.1.0/permission3
catalog entry into both Go and Hono, and proved exact install/session scopes.
Twenty combined Planner/official-app database tests, catalog parity, typecheck and
build passed. The SDK198 contracts and all 74 public HTTP contracts are unchanged.

## Calendar events and agenda

Calendar event list/create/update/delete and agenda.list now use the native
repository and public contracts. App event reads/writes require calendar.read
or calendar.write respectively; agenda requires tasks.read. Current Space
membership and tasks.view/manage permissions are checked in the transaction.
Conversation events require human membership in the same Space conversation.

Event creation normalizes title, text, timezone and status and assigns the creator
on the server. Edits and archive use optimistic versions and return 409 on a stale
version, including repeated archive. They cannot change audience or creator via
read-only body fields. Event mutations and the existing Space notification commit
atomically. Zero-duration events remain accepted, matching Go. List results retain
Go's imported-then-native grouping and include canceled events.

Agenda excludes canceled tasks/events and archived entries. It includes due tasks,
imported events, native events, roadmap milestones/goals and eligible nodes.
Timestamp sorting and overlap comparisons run in PostgreSQL; roadmap date bounds
preserve the input offset's calendar date. Archived milestones exclude their goals.

Four new PostgreSQL cases exercise all five SDK methods and public response
schemas, direct aliases, optimistic conflicts, App scopes/private audience,
combined agenda data, interval validation and atomic event rollback. All fourteen
Planner cases and typecheck/build pass. Schema remains API167 and public contracts
remain SDK198/74. This is handler verification, not provider synchronization or
complete realtime delivery.

## Remaining implementation

The task consumer now creates assigned-Agent runs/jobs and pending workflow
claims. Agent/workflow execution itself remains R4 work. Explicit Calendar source
synchronization, watch callbacks and periodic reconciliation are now implemented.
Legacy Calendar ownership handover remains R10 work. Roadmap CRUD and graph reads
and milestone/goal/task-link mutations are implemented; node/definition/edge/layout
mutations remain. Task batch operations, roadmap graphs
and realtime/background delivery also remain open.

The API remains unready and Go remains deployed. Final local assembly and bounded
integrated verification remain required. Broader load/fault qualification is
follow-up under the user's revised implementation-first priorities; it does not
justify delaying unrelated handler ports or SDK App integration.
