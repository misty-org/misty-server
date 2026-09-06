# Native account export

`POST /me/export`, `/api/me/export` and `/v1/me/export` now return the existing
format-v2 manifest after account-session authentication and password confirmation.
The public SDK receives no new method. The response retains `account_data`,
`documents` and `assets`; `account_data.journal` and `account_data.assets` are null
because the signed descriptors occupy the top-level arrays.

## Data and access

The manifest includes the account profile and settings, current active Space
memberships, authored messages, owned agent definitions and versions, public
connected-account metadata, authored active notes/drawings, and authorized Journal,
Library and message-attachment downloads. Format v2 retains the existing empty
agent `space_memberships` array and does not export runtime conversation/memory
logs. It is not a full database backup or a newly expanded export schema.

SQL projections explicitly omit password hashes, sessions, OAuth credentials,
private object-key fields and billing records. Raw authored JSON is streamed from
PostgreSQL without parsing/re-encoding, preserving numbers outside JavaScript's
safe-integer range and user-supplied null values. Agent optional fields retain Go's
omission behavior. Descriptor byte sizes and ACL versions reject unsafe numeric
precision. Dates retain their instants; PostgreSQL JSON timestamps may use `+00:00`
where Go uses `Z`.

Compared with the Go source, the native path closes several authorization gaps:
Journal assets require an active parent and current active Space membership;
authored messages and all attachment types require current Space access; private
conversation checks bind the conversation to the same Space. Deleted/archived
Journal parents and former memberships cannot mint download capabilities.
These intentional access changes need representative-data/client review before
endpoint ownership changes.

Journal descriptors use the existing Ed25519 signer with a 15-minute viewer ticket,
`export=1` and the same room derivation. The managed Worker checks ACL versions,
single-use ticket replay and deleted resources. Object descriptors use the existing
local S3 presigner with a 15-minute expiry and explicit filename/MIME/size/digest.
An unconfigured required signer returns a whole-response 503. Self-host account
access remains subject to the existing entitlement gate. As in Go, filesystem-only
object stores do not implement direct presigning; exports with such assets return
`account_export_assets_unavailable` until a supported transfer path is configured.

## Snapshot, bounded resources and cleanup

Password verification occurs outside the export transaction. The transaction then
locks Spaces in sorted order, rechecks the exact account/session/password hash, and
holds content locks while generating the manifest. Asset rows precede file rows,
which precede blob rows. Repeatable-read isolation gives profile, authored content
and metadata one database snapshot. The final wall-clock session check and operation
cancellation check precede commit. Concurrent reset/deletion/revocation cannot
turn an earlier password check into a fresh export. A conflicting snapshot or lock
timeout returns 503 without automatically replaying the export.

The complete JSON file is built before sending headers. PostgreSQL cursors fetch
one row at a time; authored JSON remains raw. A temporary directory is private and
the manifest is opened exclusively with mode 0600, then immediately unlinked.
The open descriptor holds the contents until response completion, cancellation,
failure or process exit. Admission remains occupied until generation/transaction
cleanup and descriptor closure have both finished. Completed responses release the descriptor before signaling
end-of-stream. No public file path, download token or durable export record is made.
No full manifest is retained in the JavaScript heap.

Each service instance permits two exports, including responses still being streamed.
Account attempts share a five-per-minute sliding window across aliases. The JSON
request is limited to 4096 bytes. Generation checks a 60-second deadline between
operations, while individual SQL statements have a five-second timeout and locks
a two-second timeout. Pool acquisition retains its configured five-second bound.
Streaming expires after five minutes. Response reads use 64 KiB chunks.

A database row is capped at 16 MiB of serialized JSON and a complete manifest at
512 MiB. Exceeding either rejects the entire response with `account_export_too_large`;
rows are never silently truncated. Disk/quota/file-descriptor exhaustion becomes
503. Provision private temporary storage for two concurrent maximum-sized exports
plus other runtime temporary work. A tmpfs consumes container memory and must be
included in the memory budget. The small image smoke tmpfs verifies functionality,
not maximum export capacity. Larger accounts or longer snapshots require a reviewed
asynchronous/paginated export design before claiming unrestricted production scale.

## Local verification and release gates

Eleven restricted-role PostgreSQL tests cover aliases, credentials/passwords, all four
asset types, own/foreign authored data, optional fields, exact raw JSON numbers,
private/archived/former membership exclusion, signed viewer claims, missing signers,
password/session races, real row-lock contention, admission, cancellation, a complete
250-message multi-megabyte history and no partial response after late signing failure.
Three file tests cover multi-chunk UTF-8, exact lengths, cancellation, stalled reads,
size limits and one-time descriptor cleanup. The Go portable-export database test
also passes in its separate disposable database; this is source-behavior evidence,
not an identical cross-runtime database snapshot comparison.

The local managed Worker accepts native export tickets for both notes and drawings,
returns the persisted Yjs content and rejects replay. This uses the same signer as
the endpoint. Full API/PostgreSQL/R2/Worker/network export from a representative
account, Windows/mobile client handling, self-host asset delivery, maximum-size
memory/disk/lock measurements and operational cancellation/crash rehearsal remain
release gates. No production data was exported and no Go endpoint was switched.

Account deletion initiation/status and queue foundations are now tested separately;
see `account-deletion-cutover.md`. Provider/billing cleanup, local cleanup, final
purge and production activation remain unfinished.
