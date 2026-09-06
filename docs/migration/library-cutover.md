# Native Library migration

Core reads, downloads, item mutations and organization are composed in the private Hono API. Go still owns the deployed
Library and its remaining mutations, transfers and jobs. This checkpoint is not
a readiness or retirement declaration.

| Operation | Native behavior |
| --- | --- |
| GET /spaces/:spaceID/library | Existing search syntax, filters, sorting, pagination and file/item DTOs |
| GET /spaces/:spaceID/library/items/:itemID | Account, Space, audience and sensitive-item authorization |
| GET /spaces/:spaceID/library/facets | Counts, tags, media types/subtypes, years, albums and utilities |
| GET /spaces/:spaceID/library/usage | Owner balances and reconciliation; member availability only |
| POST /spaces/:spaceID/library/reauthenticate | Password verification, five-minute hashed grants, scope and audit |
| GET /spaces/:spaceID/library/items/:itemID/download | Original/current rendition, signed descriptor or local stream, views and egress limits |
| PATCH /spaces/:spaceID/library/items/:itemID | Metadata replacement, version conflict and audit |
| POST /spaces/:spaceID/library/items/bulk | All14 existing actions, atomic versions/audits and requested item order |
| POST /spaces/:spaceID/library/items/:itemID/trash | Thirty-day recovery and contribution transition |
| POST /spaces/:spaceID/library/items/:itemID/restore | Scoped reauthentication, recovery expiry and contribution restoration |

All operations retain bare, `/api` and `/v1` aliases. Library uses account sessions;
no public SDK method or App scope is added. Files remains unpromoted. Modules are
under `apps/api/src/modules/library`: routes, repository, model, search, filters,
facet SQL and reauthentication. SQL is parameterized and runs through the shared
transaction/permission infrastructure. Read snapshots have bounded statement and
lock timeouts; grant issuance rechecks authorization and password after hashing.

The implementation follows the existing Go Library queries and response shapes.
Three concrete access corrections are intentional: structured hidden filters and
`visibility=all` require a hidden grant; conversation-private items are filtered
from facets; album counts exclude inaccessible/hidden/trash rows. Item pagination
also checks the cursor's audience. Trash uses `recently_deleted` grants, consistent
with sensitive item detail; hidden and trash grants cannot be substituted. Grant
checks use the request's `X-Misty-Library-Reauthentication` header and never return
stored token hashes. File responses do not contain storage object keys.

Validation: four focused PostgreSQL integration cases pass with a restricted
NOLOGIN/NOBYPASSRLS role against local schema168. They cover aliases, DTOs, search,
pagination, private audience/facet counts, permission revocation, password denial,
scope/account/expiry rejection, audit records, and owner/member storage behavior.
Typecheck/build and generated registration coverage pass. Fixture digest and
permission-column mistakes in the first test run were corrected before rerunning.

Downloads now use `download-repository.ts` and `downloads.ts`. They require both
Library view and download permission, an accessible item, its sensitive grant if
applicable, and ready file/blob state. Current downloads choose ready renditions;
`version=original` retains original bytes/name. Queued/processing edits fall back
to the original. View counters use the existing transactional upsert.

S3/R2 always signs through the existing store. Local Go-format `.blob`/`.json`
objects stream through the filesystem adapter, with64 KiB buffering,16 concurrent
operations and a five-minute transfer deadline. Cancellation and EOF release the
file; metadata mismatch returns409 before streaming. Byte count and SHA256 are
verified while streaming, withholding the final chunk until verification so a
Content-Length client cannot accept corrupt completion. Missing objects return404.
Signed descriptors carry X-Misty-Signed-Download:1; actual JSON file bytes do not.
All successful downloads use private/no-store and safe Content-Disposition.

The existing two-minute default signed URL lifetime and R2 lifetime configuration
are reused. Egress defaults remain25 GiB/account and200 GiB/process per rolling day,
with existing environment overrides. Checks now include the new file size before
approval, and capacity saturation fails closed. This explicitly corrects Go's
single-file overshoot and saturated identity-map bypass. The guard is process-local,
as in Go; remaining transfer families must share it during assembly.

Additional validation brings the Library suite to seven passing PostgreSQL cases
and storage/egress to12 passing unit cases. Tests exercise signed links without
network calls, local streams, rendition selection/fallback, permission and quota
denials, view records, mismatch/absence,20 MB streaming, cancellation and checksum
failure before final delivery. Typecheck/build and refreshed coverage pass. SDK202
contracts are synced and HTTP count remains74; Files catalog/scopes are unchanged.

Item mutations now use `mutations.ts`, `mutation-model.ts` and `bulk-actions.ts`.
The four routes require current Library view/edit permission and item audience.
Sensitive grants are checked against locked current item state. PATCH retains Go's
replacement defaults (omitted caption/tags/flags reset), Unicode length limits and
case-insensitive input tag normalization. Stale versions return409 version_conflict.
Bulk operations accept1–200 distinct item/version pairs and preserve request order.

All14 actions are ported, including album add/remove semantics (album version
changes, item versions unchanged), flags, tags, date/location and trash/restore.
Transactions lock items in stable order and include mutation, contribution changes,
audits and response reads. Trash changes active contributions to recovery for30
days; restore requires a recently_deleted grant and an unexpired recovery window.
Neither transition frees billable bytes, and no blob deletion is performed.

The Library PostgreSQL suite now has11 passing cases. New scenarios exercise all
bulk actions, metadata normalization/defaults, permissions, private-item denial,
version conflicts, recovery expiry/accounting and rollback after SQL audit failure.
Typecheck/build and coverage pass. There are30 composed Library aliases; broader
replacement, job ownership and release readiness remain incomplete.

Organization now lives in `albums.ts`, `album-items.ts`, `album-folders.ts`,
`groups.ts`, `organization-model.ts` and `organization-routes.ts`. Seventeen
operations add51 aliases: album CRUD/settings/member list/add/remove/reorder,
folder CRUD, and rule-based group list/create/items. Existing version and order
semantics are retained, including partial album reorder, idempotent membership
add, album-version changes for membership and500-album/100-group/12-rule limits.
Folder deletion cascades to child folders but only clears album folder references;
albums and Library files survive. Writes retain existing audits where Go emits
those audits; membership add/remove and group create keep their existing behavior.

Album counts and cover selection now check private conversation audience. Add,
remove and reorder cannot mutate inaccessible item membership. Existing hidden
and lifecycle checks remain. Native organization writes serialize by Space for
ancestry/limit checks, preventing concurrent opposite folder moves from making
a cycle. Read/write responses use the same transaction's data. These are local
native guarantees; Go still needs retirement and ownership handover.

The Library suite now has16 passing PostgreSQL cases. New checks cover all17
organization operations, DTOs, ordering, counts, covers, nested folders and cascade,
concurrent moves, private visibility, versions, duplicate names, cross-Space
references, edit revocation, album quota, rollback and rule-based group evaluation.
Typecheck/build and coverage pass. Library has81 composed aliases at this checkpoint.

Next: asset-stack and edit-version metadata operations. Rendering, uploads,
previews, import/export, search indexing and retention/processing jobs remain
incomplete. No real object-store or provider request, deployment, image
qualification or Go shutdown occurred here.
