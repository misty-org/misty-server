# Native connected accounts

The private connections module serves list, removal and authorization under bare,
`/api` and `/v1` aliases and through the named `connections.list`,
`connections.remove` and `connections.authorize` SDK methods. Integration list/bind
are native too. All74 public HTTP SDK methods now have native handlers; this does
not imply complete non-SDK REST or background/runtime integration. The native private token broker
refreshes credentials for mail adapters. Native authorization/callback behavior and migration 156 are described in
`connection-authorization.md`. Go remains deployed pending the full cutover. Hono never forwards unmigrated operations to Go.

## Access and metadata

List queries select only public account identity, provider, capability/scope,
health and expiry columns. They exclude revoked connections, preserve the Go
provider/display/ID order and omit empty optional fields. Provider availability
uses the existing six providers' client-ID/client-secret environment pairs and
returns only booleans. Credentials, nonces, key versions and ownership columns
are never selected by the read path.

Account requests require an active account. App requests additionally lock the
current active Space, membership, installation and exact session grants inside
the domain transaction. List requires connections.read; removal requires
connections.write. Account-level connections remain owned by that account,
independently of which Space launched the App. A grant never permits removing
another person's connection.

## Removal and provider effects

Removal returns an empty 204 with the existing X-Misty-Provider-Revocation header.
Missing, foreign and previously revoked IDs return 404. Local erasure clears both
ciphertext and nonce and records revoked status/time. For Figma, hooks, shared
resources, integrations and bindings are disabled in that same transaction,
restricted to the connection and its owner. Failure in any local SQL rolls these
changes back before any provider request is attempted.

Google revocation uses its fixed HTTPS endpoint and form-encoded access token.
Figma webhook deletion uses its fixed HTTPS origin and individually encoded IDs,
at most four concurrent requests. There are no redirects or automatic retries;
all provider cleanup shares a 15-second/request-cancellation signal. Responses
are canceled without buffering or logging their contents. Provider failure or an
unreadable legacy credential still permits local erasure, preserving Go behavior.
Microsoft and the other existing providers retain their local-only removal behavior.

The connection row remains locked through the bounded provider attempt so a
concurrent refresh or OAuth upsert cannot replace the credential being removed.
This deliberately holds a transaction/connection during that attempt, bounded
below the runtime's 30-second idle transaction timeout. Pool saturation under
concurrent disconnects must be measured before cutover. Remote revocation and
PostgreSQL commit cannot be atomic: a commit failure after provider success can
leave locally retained but remotely invalid credentials. Never automatically
replay this mutation against another implementation. Durable provider lifecycle
reconciliation and broader account-deletion behavior remain release work.

## Encryption and configuration

Production API startup requires the existing SPACE_LINK_ENCRYPTION_KEY. Keep the
same value as Go when reading migrated credentials. The loader preserves Go's
32-byte base64, hexadecimal and literal UTF-8 key formats. AES-256-GCM uses the
existing 12-byte nonce, appended 16-byte tag, version 1 and provider-bound
`misty-connected-account-v1:` associated data. Unsupported versions, malformed
nonces, corrupt tags and plaintext envelopes larger than 2 MiB are rejected
(ciphertext permits the additional 16-byte authentication tag). The private bound
accommodates a new response plus an omitted refresh token retained from the old
credential; provider token HTTP responses remain capped at 1 MiB. This retains
the legacy format; it does not claim a new per-account encryption format or key
rotation protocol. Without configuration in development, list remains available
and removal returns 503 without changing data.

## Native token broker

The private broker supports reviewed mail read/write purposes, mapped to the
corresponding App scope and the provider's mail capability. It checks active
account/Space, installation/session, connection ownership and Google/Microsoft
provider before decoding credentials. No public route returns a token lease.

Refresh holds the connection row lock, so parallel native requests reuse the
persisted replacement instead of independently rotating one credential. Normal
one-hour tokens retain the five-minute refresh margin; short-lived tokens use a
margin capped at ten percent of their lifetime. Refresh requires a positive
expiry. Missing replacement refresh tokens retain the previous value; returned
replacement tokens are encrypted and committed before use. Expected failure
health commits before an error is returned. Transient failures have a 30-second
retry pause; invalid_grant records reauthorization_required and stops retrying
until consent replaces the credential. Missing server client configuration does
not mark the user's credential unhealthy.

Provider token exchange uses fixed endpoints, private client authentication,
form encoding, no redirects/retries, a 20-second/request-cancellation signal and
bounded streamed JSON. Error bodies and arbitrary exception text never become
application errors. Token and identity exchange remain private infrastructure. Figma refresh uses its
documented `/v1/oauth/refresh` endpoint and private Basic authentication. Protocol
references are [Google's web-server OAuth documentation](https://developers.google.com/identity/protocols/oauth2/web-server)
and [Microsoft's refresh-token documentation](https://learn.microsoft.com/nb-no/entra/identity-platform/refresh-tokens).

A fingerprint of the encrypted credential fences provider health reports and
post-request access checks. A late failure cannot overwrite a newer credential's
health, and a completed read is not returned after its App or connection has been
revoked. If a caller cancels after a replacement is received, the replacement
still commits but no private lease is returned. SQL failure after provider rotation
does not return an unpersisted token. As with revocation, provider rotation and
database commit cannot be atomic; loss of a rotated token during database failure
may require fresh consent. Recovery/reconciliation and pool-pressure tests remain
release work, not guarantees supplied by these local checks.

## Local evidence and remaining work

Six restricted PostgreSQL HTTP/RPC tests cover metadata privacy and aliases,
exact scopes, ownership, revoked grants/membership, provider failure, empty 204,
local credential erasure, real row-lock contention, and Figma cleanup/rollback.
Five unit tests cover cross-language encryption fixtures, key formats, provider
flags, fixed endpoint/encoding policy, malformed credentials and bounded Figma
requests. Go verifies the same AES-GCM fixtures in both directions and its actual
removal RPC handler rejects a read-only App and foreign connection. All provider
credentials in these tests are synthetic; no real connection is removed.

Six more restricted database tests exercise exact broker grants, twelve concurrent
requests sharing one refresh, encrypted rotation, retained refresh tokens, stale
health reports, persistent failure/cooldown, invalid_grant, malformed credentials,
cancellation, short-lived token reuse and SQL failure after receiving a replacement.
Three HTTP-client unit tests cover authentication/PKCE parameters, bounded
streaming, safe errors and cancellation of a real stalled local HTTP response.

Full checks pass 154 unit tests and 201 database tests across 23 files and all 156
migrations. API hosted/self-host and payments images pass the isolated smoke
recorded in image-smoke.json. The API image contains no Stripe package.

Ten additional database tests cover native OAuth issuance, all six provider
callbacks, replay/denial, state/client/session binding, revocation during consent,
RLS, self-host entitlement, partial grants and persistent issuance limits. Six
additional unit tests cover URL/PKCE policy, scopes, bounded identity transport and
explicit Instagram permission evidence. No real provider account was connected.

Provider resource binding, account/provider lifecycle reconciliation, live consent
and token renewal, and concurrency/load testing remain incomplete. Go stays deployed and native
readiness stays false until the entire server migration is complete.

## Space integration listing and Calendar binding

The named integrations.list and integrations.bind methods are composed in main
alongside all REST aliases. Listing requires active membership and, for Apps,
connections.read for the exact Space. It preserves provider/display/ID order and
all statuses, selecting only public metadata. The availability catalog retains
the current Go catalog (GitHub configuration flag); connected-account provider
availability remains its separate six-provider map.

Binding requires integrations.manage, connections.write for installed Apps, an
owned unrevoked active Google connected account and its requested calendar_read
or calendar_write capability. Bare REST defaults an omitted capability to
calendar_read, matching Go. Plaintext stays private and is re-encrypted from
misty-connected-account-v1:google to misty-provider-v2:google using the existing
key. Scope/expiry/account metadata are preserved without a provider request or
token refresh. Normal Calendar synchronization handles refresh later.

The source credential is locked while the transaction upserts integration and
Space credential rows. Failed persistence rolls everything back. Rebinding uses
the existing Space/user/provider/display identity and retains credential row IDs.
The private credential_reference is explicitly set to that retained ID, fixing
the concrete Go upsert inconsistency that pointed at a newly generated unused ID.
Responses never include credentials or that private reference. Provider copies
and their later lifecycle/reconciliation remain governed by the existing separate
Space credential model; complete provider cleanup/handover remains R2/R6/R10.

All25 connection PostgreSQL cases pass, including3 added binding/listing flows,
public SDK result parsing, all REST aliases, encrypted AAD isolation, stable
references on repeated binding, current permission/capability/ownership/scope
checks and SQL rollback. Typecheck/build pass; the corrected RPC list assertion
was rechecked separately. SDK201/schema168 remain unchanged. No live provider
operation, whole-suite/image run or deployment occurred.
