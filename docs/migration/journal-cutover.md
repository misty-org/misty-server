# Native Journal migration

The Hono API implements twenty note/drawing methods: fourteen CRUD, backlinks and
collaboration-ticket methods plus six asset transfer methods, and signed note projection callbacks. They
mount under bare, `/api` and `/v1` aliases. The native RPC dispatcher reuses the
same HTTP authorization and validation through the shared native dispatcher.
Space reads are also native; remaining methods return an explicit 501;
there is no mutation fallback to Go. Go remains the deployed owner.

## Authorization and transactions

Every operation checks active account state, current Space membership, the
resource's actual Space and its conversation audience when present. App requests
also require the corresponding read/write scope, bound Space, current installation
and exact live credential under transaction locks. Existing membership semantics
give creators their creator role and other authorized members editor access.
Only creators and Space owners may archive/delete; a Space owner still needs
access to a conversation-scoped document. Viewer ticket support is retained for
internal read/export flows, without adding a new public permission model.

The service RLS context is used with explicit actor checks because the historical
note policies predate the current membership model. Policies are not relaxed.
Lock order is Space, account, installation/session, membership, document and its
conversation membership. Events, notifications and control-outbox commands commit
with the mutation. Missing-resource/audience checks do not disclose content.

Native archive/restore deliberately increments the ACL version and queues an ACL
control command. This strengthens the legacy archive behavior, which did not
retire existing sockets. Note deletion queues purge and marks assets for deletion;
drawing deletion also marks assets for deletion. An outbox failure rolls the mutation back.

`MISTY_NATIVE_JOURNAL_JOBS=1` enables native control-outbox delivery and document
retention. ACL, disconnect, purge and note-bootstrap commands use bounded signed HTTP
requests, semantic acknowledgements,
two-minute claims, generation-fenced completion and retry backoff. Purge waits on
active document holds. Metadata deletion requires confirmed room purge, all assets
retired, no active upload reservations and no document hold. Parent locking before
the final check prevents a concurrent reservation from being missed. Candidate
selection excludes blocked documents so they cannot starve eligible cleanup.
Leave this switch off while Go owns these jobs.

## Ticket and projection protocol

Tickets preserve Ed25519 signing, issuer/audience, user/Space/resource/type, opaque
HMAC-derived room IDs, creator/editor/viewer roles, ACL version, single-use IDs,
60-second join deadlines and `/parties/{note|drawing}-room/{room}` URLs. Internal
export tickets are read-only and have the existing fifteen-minute deadline.
Ticket responses are never cached. A join deadline does not expire a live socket.

Projection callbacks verify the HMAC against the exact bounded request bytes and
a five-minute timestamp window before parsing. Active and previous projection
keys support rotation. Only increasing revisions on active notes update content;
duplicate or stale callbacks return `applied: false`. Content, same-Space active
backlinks and the projection event commit together. Event failure rolls back all
three. Body, title, text and link counts are bounded.

Production startup requires valid Journal configuration: the existing base64 DER
PKCS8 `JOURNAL_COLLAB_TICKET_PRIVATE_KEY`, base64 room salt, control and projection
secrets (at least 32 bytes each), with optional previous projection secret. The
worker receives the matching public key and shared secrets. Hosted origin uses
`PARTYKIT_HOST`; self-hosted deployments may use `MISTY_COLLAB_PUBLIC_URL` with
HTTPS or loopback HTTP. Development without keys supports CRUD but rejects ticket
issuance with 503. No private signing key belongs in the SDK or downloaded apps.

## Local evidence

Nine restricted-role PostgreSQL HTTP tests cover both resource types, existing
response schemas, aliases, CRUD, RPC, scope/Space/session revocation, conversation
visibility, cross-Space denial, destructive permissions, archive idempotency/ACL
updates, outbox rollback, signed projections, key rotation, monotonic revisions,
backlinks and transaction rollback. Two unit tests use the actual managed worker's
ticket verifier and socket policy against native-issued tickets.

`scripts/migration/check-journal-runtime.mjs` uses the compiled native ticket issuer
and bundled managed worker in Miniflare over real WebSockets. For both document
types it checks editor synchronization, viewer write rejection (including a fresh
observer), ticket replay rejection, stale-socket disconnection after an ACL command,
stale-ticket denial and reconnect at the new ACL version. The native control sender
also performs purge, retry and post-purge join rejection. The worker's four separate
runtime tests cover persistence across restart and purge without resurrection.
The cross-application check stubs outbound projection delivery; PostgreSQL callback
tests above verify the receiver separately. It does not establish self-host parity
or an end-to-end production outbox run; restricted PostgreSQL tests separately
verify claim contention, failure retries, stale-acknowledgement fencing and held
resource deferral.

Run `npm run build`, install the collaboration app's locked dependencies and run
`npm run test:runtime` there, then run
`node scripts/migration/check-journal-runtime.mjs` from the repository root. CI runs
that same sequence. All test keys and rooms are disposable and local.

## Remaining gates

The SDK task has now verified the signed Journal 1.1.0 candidate with two actual
SDK clients, the native Hono API, restricted PostgreSQL and the actual managed
Worker in Miniflare. Signed projections reached PostgreSQL, and the native control
outbox disconnected archived documents. The separate macOS host check covered
Discover install, account/App session HTTP, navbar opening, real title editing
through projection, independent Notes/Drawings panels, and uninstall cleanup.
Account/Space shell bootstrap was seeded and panel creation used the workspace
store. It is not a production deployment or multi-account/Worker-restart proof.
The same archive separately passed native clipboard, real folder picker and SVG
export checks. See `journal-sdk-host-proof.json` and the SDK-owned desktop
`docs/architecture/app-platform/implementation.md` for scope and reproduction.
The local Go/Hono catalogs now both record Journal 1.1.0, permission 3, protocol 2
as downloaded on desktop and embedded on mobile. Existing running Go binaries
must be rebuilt/restarted through their normal local workflow to load that snapshot.

Ordered, replay-safe note-content replacement control delivery, self-host state persistence callbacks,
resource/account/Space lifecycle integration, exports/imports and failure/load
coverage remain. The SDK task has implemented host-owned bounded asset transfers and
opaque collaboration handles; raw tickets, signed URLs and finalize credentials
must remain inside that host boundary. The six asset contracts now appear in both
registries and the exhaustive native scope map (65 HTTP methods total). Both RPC
dispatchers allow `X-Misty-Library-Upload-Token` only for note/drawing finalize;
tokens must contain 1–1024 printable non-whitespace ASCII characters, with commas
and duplicate values rejected. Arbitrary headers and account cookies are not
forwarded. The six native asset methods now pass SDK-schema HTTP/RPC tests against
restricted PostgreSQL. See `storage-cutover.md` for signing, quotas, cleanup and
remaining storage ownership gates.

The temporary Go owner now binds the upload's stored parent to the URL and checks
current parent access, including inside completion before the already-ready path.
Generic Library routes cannot finalize Journal assets. Real Go HTTP/PostgreSQL
tests exercise all six new methods through app RPC with local object storage:
reserve, missing/malformed credential denial, wrong-parent denial, finalize,
idempotent retry, signed download and retired-parent rejection. These fixtures
establish legacy compatibility; they do not establish native asset parity or
concurrent revocation correctness for the existing Go storage transaction design.

Current collaboration tickets do not identify the app installation, so revoking an
app token alone cannot terminate an already-open socket. Host teardown/leases and
server account/resource disconnect controls must be validated together. Journal is
still embedded in the reviewed catalog. No app promotion, public package publication,
deployment or endpoint ownership switch has occurred.
