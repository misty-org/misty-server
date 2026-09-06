# Native account avatars

The Hono API now implements account avatar upload/read and Space-member avatar
read through `/v1`, `/api` and bare aliases. This does not replace personal-agent
avatars, Library/agent attachments, the account summary or account deletion/export.
Go remains deployed; endpoint and background-job ownership have not changed.

## Publication and authorization

`PUT /me/avatar` requires a current account session. Downloads require that session
or, for `/spaces/:spaceID/members/:userID/avatar`, current Space membership. App
credentials require an explicit Bearer header, work only on the member route, and require the exact Space plus
`spaces.read`. Every transfer checks authorization before object access and again
before returning bytes or publishing the new pointer. Self-host entitlements and
administrative disablement are rechecked at both boundaries.

Each upload writes a fresh `avatars/avatar_<uuid>` object. Migration
`20261205000000_avatar_object_publication.sql` adds a nullable user pointer and
cleanup ownership. A committed deletion intent precedes the PUT. Only a successful
PUT followed by a current-credential transaction can increment the avatar version,
publish the pointer and consume that intent. Failed writes, revoked sessions,
expired staging deadlines and failed commits retain the old avatar and cleanup
intent. The account row serializes publication without holding a SQL transaction
over storage I/O. Concurrent successful uploads get distinct versions and objects.

A restricted trigger queues replaced objects, including legacy fixed keys, with
a five-minute grace period. Pending account deletion retains the current avatar;
final user deletion queues it without retaining a deleted creator. The deletion
worker checks current user references as well as existing Library holds/references.
New uploads register a fifteen-minute staging deadline, longer than the twenty-
second upload operation budget. A claimed or expired cleanup intent cannot be
published. Twelve outstanding objects per account bound accumulated failed and
replaced uploads across replicas; excess requests receive 429. The two concurrent
upload permits and eight read permits are shared across all path aliases.

## Image and transport boundaries

Uploads retain the existing five-MiB PNG and 4096×4096 dimension limits. The native
parser matches Go's `image/png.DecodeConfig`, including color/depth, CRC, palette,
interlace metadata and chunk ordering. It reads chunk names byte-for-byte and never
inflates pixels. Go accepts some header-only images at this boundary; native parity
does too. This is metadata validation, not a claim of full image decoding or
sanitization. `pngjs` is used only by development fixtures, absent from production
dependencies. The implementation uses Node's built-in CRC function.

Responses preserve `image/png`, private five-minute caching and version ETags,
with `nosniff`. An immutable key keeps returned bytes paired with their version
during replacement. Existing cached responses retain their original private cache
lifetime; authorization revocation applies to new server requests.

The S3 adapter sends authenticated server-side PUT/GET operations. PUT binds size,
MIME and SHA-256; reads bound declared and actual stream length and verify stored
checksum metadata when present. Sixteen active storage operations, ten-second
deadlines and bounded SDK retry settings apply. A streaming GET retains its permit
until the stream ends and abort explicitly cancels its reader. Storage credentials,
object keys and checksum metadata are never returned by avatar routes.

## Self-host filesystem storage

Explicit `MISTY_LIBRARY_BACKEND=filesystem`, or a configured local directory without
an explicit backend, selects local storage. `MISTY_LIBRARY_FILESYSTEM_DIR` takes
precedence over the legacy `MISTY_LIBRARY_LOCAL_DIR`. An explicit `s3` overrides
local directory settings. Hosted production rejects local storage; self-host
production uses an operator-controlled persistent directory mounted writable for
the non-root runtime. The tested production platform is the Linux container.

The adapter retains Go's layout: avatar keys become `<root>/avatars/<id>.blob` and
`.json`, while `library/` is stripped before forming Library filenames. JSON keeps
Go's `ByteSize`, `SHA256` and `MIMEType` fields. Files use mode 0600 and newly created
directories use 0700. Fresh-key exclusive creation prevents replacement of existing
objects. File and directory synchronization precede successful publication; no
untracked temporary object names are used, so a partial write remains addressable
by its durable deletion intent. A 64-MiB free-space reserve matches Go's local
capacity check; write failures still leave cleanup work for retry.

Reads bound metadata to 4096 bytes, validate file size and checksum, reject
non-regular files, and use no-follow/non-blocking opens. Directory checks reject
substituted symlinks. The configured directory and its ancestors must be controlled
by the operator; this does not isolate against an administrator concurrently
replacing the underlying filesystem. File operations follow the
[Node filesystem APIs](https://nodejs.org/docs/latest-v24.x/api/fs.html), including
exclusive creation, bounded reads and explicit synchronization.

This adapter supplies private small-object transfers and queued deletion. It does
not add signed HTTP upload/download routes for filesystem Library transfers.
Filesystem Journal direct transfers remain unavailable, matching Go's existing
S3-only direct-transfer requirement.

## Evidence and ownership gates

Eleven restricted-role PostgreSQL tests cover aliases and byte/version pairing,
legacy keys, hard deletion, failed PUT/commit, concurrent publication and admission,
logout and member/App revocation, reference-protected cleanup, per-account bounds,
real filesystem HTTP publication/cleanup, self-host disablement/expiry and expired
staging intent. Shared PNG fixtures pass both the actual Go validator and native
validator. Seven filesystem tests cover corruption, partial objects, permission
modes, reopening, overwrite/traversal/symlink denial, cancellation and configuration.
Six S3 tests cover signatures and real local authenticated HTTP operations,
oversize/corrupt responses, streaming admission and cancellation.

`node scripts/migration/check-avatar-storage.mjs`, after `npm run build`, writes
with native code and reads with the actual Go local adapter, then writes with Go
and reads with native code. It uses a disposable private directory, checks bytes
and metadata for both filename layouts, and cleans up. CI runs the same proof.
The Go database contract also verifies reading a native avatar reference and
returning to a legacy key while retaining the replaced-object cleanup intent.
Image smoke now includes self-host filesystem startup and private write/read/delete
under Node 24 on Linux as UID 1000, with a read-only root filesystem and no network.
See `image-smoke.json` for exact artifacts and timestamps.

Before cutting over these routes, apply migration 157 and provision its role
grants, verify private production storage, and assign cleanup ownership. Native
avatar publication needs its deletion queue drained; leaving
`MISTY_NATIVE_STORAGE_JOBS` off indefinitely intentionally reaches the outstanding-
object limit. That flag also owns Journal expiration and asset retention, so
coordinate the complete worker ownership change instead of enabling duplicate
workers. Production storage failure/load and interrupted-process rehearsals remain.

Rollback must use the compatible Go build that reads `avatar_object_key`; an older
Go image cannot resolve native pointers. Preserve the additive migration. Stop and
drain native writers/cleanup before routing writes back to Go's legacy fixed keys,
which can otherwise race a previously approved deletion. Native cleanup must not
overlap that ownership transition. The Go legacy upload algorithm itself is not
made immutable by this compatibility patch. No live storage, deployment, worker
switch or full rollback rehearsal is claimed by the local tests.
