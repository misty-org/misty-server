# Native storage and Journal transfers

Hono now serves Journal image reserve/finalize/download for notes and drawings,
under the existing HTTP aliases and six public RPC methods. Go remains deployed;
this is not a complete Library, agent-avatar or attachment migration. Account
avatars now have separate native routes; see `avatars-cutover.md`. The API owns the
object-store client and credentials. Payments has no S3 client dependency.

## Transfer and data behavior

The server accepts passive raster formats with the existing 15 MiB maximum and
configured lower note/drawing limits. It sanitizes filenames, validates hashes,
requires the drawing scene file ID and binds every upload to its actual parent,
Space, contributor and purpose. App grants and exact credentials are rechecked
under locks, including after external object verification and on ready retries.
Conversation audience checks apply at reserve, finalize and download.

The S3 adapter uses pinned AWS SDK v3 modules and direct signed PUT/GET operations,
following the [R2 presigned URL pattern](https://developers.cloudflare.com/r2/api/s3/presigned-urls/).
PUT signatures cover the object key, exact Content-Length, Content-Type, SHA-256
checksum and server-owned checksum metadata. Checksum headers remain signed
headers rather than being duplicated in query parameters, using the SDK's
[unhoistable headers option](https://docs.aws.amazon.com/AWSJavaScriptSDK/v3/latest/Package/-aws-sdk-s3-request-presigner/).
Browsers supply Content-Length from the bounded upload body. Misty credentials
are never part of the object transfer. Downloads have sanitized dispositions.
The bucket must be private and Library keys must not have an attachment TTL rule.

Reservations last 30 minutes. Upload URLs default to 15 minutes and cannot exceed
the reservation deadline; downloads default to 2 minutes. Existing
`MISTY_R2_UPLOAD_URL_TTL`, `MISTY_R2_DOWNLOAD_URL_TTL`,
`MISTY_NOTE_ATTACHMENT_MAX_FILE_BYTES` and `MISTY_DRAWING_ASSET_MAX_FILE_BYTES`
settings are bounded at startup. S3 configuration preserves `MISTY_S3_*` names and
the earlier `R2_*` aliases. Origins require HTTPS, with loopback HTTP permitted for
self-host development. Missing S3 configuration returns storage-unavailable for
these operations. Filesystem-backed Journal uploads remain unsupported, as with
Go's direct-transfer requirement; native filesystem Library support is outstanding.

HEAD/DELETE operations have ten-second overall cancellation, bounded retries and
at most sixteen concurrent operations. HEAD compares exact size, checksum metadata
and MIME before commit. This relies on the checksum-constrained object-store PUT;
it does not pretend that the API scanned the image body. Blob scan status is
`skipped`, preserving the Journal policy. No image body enters PostgreSQL or Yjs.

Personal allowance includes active/recovery contributions and outstanding upload
and rendition reservations across active Spaces. Space allowance uses its owner's
plan. Shared storage advisory locks serialize both dimensions, with BigInt SQL
accounting and transaction rollback on counter inconsistencies. Finalization
checks current authorization, upload token, parent and reservation again, creates
the file/asset/contribution and updates quota/events/audit together. Identical
retries return the same asset. Deduplication rechecks the current target outside
the transaction and repairs a missing object using the verified replacement.
Opaque object keys and token hashes are excluded from response rows.

The additive Go compatibility change now includes verified MIME/size/SHA in note
asset results and list/retry reloads, matching drawings and the public SDK schema.

## Cleanup ownership

Migration `20261130000000_object_deletion_jobs.sql` adds a service-only deletion
queue. Verification mismatch releases the reservation and schedules object cleanup
after the PUT capability has expired, so a late PUT cannot recreate a deleted
object. Deduplication discards and replaced missing keys use the same durable queue.
Deletion claims use expiring leases, fenced acknowledgement and retry backoff.
Referenced blobs, held document/asset/file/blob objects and keys with unexpired
upload capabilities are not deleted.

`MISTY_NATIVE_STORAGE_JOBS=1` explicitly enables the native abandoned-Journal-upload
retention and queued-object worker in the API. Leave it disabled while Go owns those jobs.
Abandoned uploads release reservations transactionally, record an audit event and
enqueue their keys. Failures remain retryable. Native asset list/delete retains
parent authorization and delays quota release for 24 hours. Retention locks and
rechecks the asset, respects document/asset/file/blob holds, releases each logical
contribution once and preserves blobs with other file/rendition/export references.
The final unreferenced blob enters the durable deletion queue. Older deleting
parents with ready assets are normalized under a parent lock. Concurrent workers
cannot double-release quota. This worker does not yet replace Library
reconciliation, scanning or rendition work. Document metadata cleanup is separately
gated by `MISTY_NATIVE_JOURNAL_JOBS`; see `journal-cutover.md`.

## Evidence and open gates

Five new restricted-role PostgreSQL tests cover all six HTTP/RPC methods and SDK
result parsing, concurrent finalize, metadata mismatch and reservation release,
deduplication repair, credential revocation during HEAD, event-failure rollback,
personal quota contention across Spaces, Space quota across contributors, expired
reservations, concurrent deletion workers and retry/reference protection. The
existing Journal authorization/projection suite still passes. S3 tests verify real
signatures and local HTTP HEAD/DELETE behavior; limits have a separate unit test.
Go HTTP/database tests check additive asset metadata and existing route behavior.
Five additional restricted-role tests cover authorized list/unlink and delayed
quota release, shared-blob retention and holds, old drawing deletion backlog,
reservation and purge gates, control retry fencing, and held-resource deferral.
Hold checks protect existing holds; concurrent hold creation must use compatible
target locking when that administrative workflow migrates.

The SDK task's actual host clients also pass a real local network run through the
native Hono server, restricted PostgreSQL and a local HTTPS object fixture. It
covers 300 KiB note/drawing uploads spanning multiple SDK chunks, finalization,
byte/hash-identical downloads, corrupted-download rejection, app revocation after
PUT but before finalize, and no further HTTP after view closure. The recorded run
made eleven RPC calls and ten object requests; see `journal-network-probe.json`.
The disposable Hono process exited successfully and its rows/configuration were
removed. The object fixture enforces the wire protocol but does not validate a
production S3 signature; separate signer tests check the signed constraints.

These tests do not prove live R2 checksum enforcement, CORS, bucket privacy,
lifecycle rules or the native desktop runtime. Those require explicit integration
evidence before release. Also
complete self-host persistence callbacks, account/Space lifecycle integration,
Library/attachments/agent avatars and filesystem Library transfers, global job ownership, production
role grants, rollback and load/failure rehearsals. Cleanup defaults and honest
readiness remain migration gates. No production ownership switch has occurred.

## Disposable host integration fixture

`scripts/migration/serve-journal-fixture.mjs` composes the compiled native API,
real account/app credentials, Space/note/drawing records and S3 adapter for a host
client integration run. It requires a separately migrated local database named
`misty_hono_journal_fixture_test` and a local HTTPS object endpoint. It reads only
a mode0600 JSON config (`databaseUrl`, `s3`, `outputPath`), not the application's
environment file. Start Node with `NODE_EXTRA_CA_CERTS` pointing at the test CA.
Only the fixture application role receives its table grants.

The private output file contains the API base, app credential, resource IDs and
fixture-control credential. These are never printed. Authenticated fixture-only
controls revoke that one app token or expire its abandoned uploads and run cleanup.
SIGTERM removes that run's database rows and output file and closes its pools.
The HTTPS object endpoint is owned and stopped separately by the SDK test runner.
This launcher is a local verification tool, not a production service or an R2
signature-verification substitute.
