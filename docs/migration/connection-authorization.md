# Native connected-account authorization

`connections.authorize` and `/oauth/connections/:provider/callback` now have native
Hono handlers under bare, `/api` and `/v1` aliases. This brings native SDK HTTP
coverage to 35 of 74 methods. Go remains deployed; these local checks do not close
the full migration or establish live provider compatibility.

## Configuration and public contract

The public SDK continues to submit capabilities and a relative return path and
receive an authorization URL and state expiry. Provider client secrets, tokens,
PKCE verifiers and SQL state never enter the SDK. The browser completion page has
no scripts, external resources, redirects or credential-bearing links. Account
display text is escaped, responses are not cached, framing is denied and referrers
are suppressed. As with Go, `return_to` is validated and stored but the completion
page asks the user to return to Misty; it does not navigate to the supplied path.

Configure `MISTY_PUBLIC_API_URL` explicitly. HTTPS is required except for exact
loopback HTTP development hosts. Supported paths are `/api` and `/v1`; an
origin-only value retains Go's `/api` default. Host and forwarding headers never
choose the callback. Production startup rejects configured OAuth clients without
this setting. An incomplete development configuration returns 503.

The existing six provider client-ID/client-secret environment pairs are retained.
Provider authorization, token and identity endpoints are fixed in private code.
The API accepts at most 16 KiB of authorization JSON after authentication. The
database serializes issuance per owner and limits it to 20 requests per ten
minutes across replicas and aliases. Eight concurrent callbacks per API process
are admitted; excess callbacks receive 503 before token exchange.

## One-use state and current access

Migration 156 adds `connection_authorization_requests` with forced RLS and an
owner/service policy. Native requests are separate from the old Go state table:
an old handler cannot consume native state while ignoring its extra checks.
Switch authorization and callback ownership together. In-flight requests from the
previous implementation must finish there or restart; never retry a callback
against the other implementation after an exchange failure.

Each request uses 32 random state bytes, stores only their SHA-256 hash, and
encrypts its 48-byte random verifier using the existing provider-bound AES-GCM
format. State binds the provider, configured client ID, exact callback URI,
initiating account or App session, requested capabilities and a credential
snapshot. State normally expires after ten minutes. App-initiated state expires
no later than its original five-minute App session; `state_expires_at` reports
that deadline. A long consent flow must restart with a renewed App session.

Consumption locks the exact unused, unexpired request and commits before any
provider call. It also erases the stored encrypted verifier. Provider denial
consumes state too; malformed, duplicate-parameter and wrong-provider callbacks
cannot consume another flow. Used states cannot exchange a code again.

The service checks the original live session, active account, current App grants,
Space and membership before exchange and again inside the credential-save
transaction. Self-host callbacks additionally check current entitlement and
disablement, even when the external browser has no desktop cookie. Uninstall,
session revocation and account deletion therefore prevent a late callback from
saving credentials.

The issuance snapshot records each existing provider account's ID, encryption
nonce and eligible capabilities, bounded to 1,000 records. Save locks the exact
identified account and compares that snapshot. Removal, refresh, another reconnect
or deletion/recreation during consent makes the old attempt fail. Concurrent
creation of the same identity uses a unique constraint without overwriting the
winner. This conservative fence works with existing Go writers as well: credential
rotation changes the nonce. Users may need to restart consent if a background
refresh occurred while the provider page was open.

Authentication cleanup removes legacy expired OAuth states and native requests
24 hours after expiry. The native retention window preserves the persistent
issuance limit even when a short App session expires. Consumed verifiers have
already been erased; expired unused verifiers remain encrypted until cleanup.

## Provider protocol and grants

Google, Microsoft, Dropbox, Figma and the existing Discord flow receive an S256
PKCE challenge; Instagram preserves its existing non-PKCE flow. Google requests
offline/incremental consent; Dropbox requests offline access and user-scope
accumulation. Dropbox's default capability is correctly `files`, fixing Go's
invalid implicit `mail` default. Figma requests previously active capabilities
alongside new ones because its tokens can replace earlier access.

Returned scope evidence replaces old grants. Candidates come from the requested
capabilities and that identity's previous eligible capabilities; equivalent scopes
do not silently add unrequested automation. Explicit declined or empty grants do
not become success. Omitted `scope` retains the requested set, matching the
existing token convention; explicit Instagram `permissions` is normalized to scope
evidence. Provider-specific omission behavior must be verified in staging.
Google documents [checking the actual grants](https://developers.google.com/identity/protocols/oauth2/web-server#check-granted-scopes).

Identity requests use fixed provider endpoints, a 15-second deadline, no redirects
or retries, and a streamed 1 MiB limit. IDs must be strings, preserving large
Figma/Discord/Instagram identities without floating-point conversion. Token
exchange is bounded to 20 seconds; the combined exchange/identity operation has a
40-second cancellation deadline. SQL runs outside provider waits. Sensitive URLs,
bodies and arbitrary provider errors are never logged or placed in callback HTML.

Fresh credentials retain the Go encryption/row format. An omitted refresh token
may be retained only from the unchanged, non-revoked previous credential. Old
capabilities are never blindly unioned into the new token's grants. Figma uses its
distinct refresh endpoint, as specified by its [OAuth documentation](https://developers.figma.com/docs/rest-api/oauth-apps/).
The other protocol references include [Microsoft authorization code flow](https://learn.microsoft.com/en-us/entra/identity-platform/v2-oauth2-auth-code-flow)
and [Dropbox's OAuth guide](https://developers.dropbox.com/oauth-guide).

## Evidence and remaining release work

The connection PostgreSQL suite passes 22 tests, including ten authorization
cases. They exercise all six synthetic provider callbacks, all REST aliases and
native RPC, encrypted state and credentials, exact redirects, HTML escaping,
replay/denial, concurrent callbacks, reduced scopes, refresh-token preservation,
removal/refresh/App revocation during consent, client-ID binding, RLS, disabled
self-host access and persistent issuance limits. Unit tests cover fixed URLs,
PKCE, default capabilities, scope reduction, large string IDs, explicit permission
evidence, bounded streams and error redaction. Cleanup tests verify retention.
The complete check passes 154 unit tests and 201 PostgreSQL tests across 23 files
and 156 application migrations.

Remaining work before deployment:

- Exercise real provider registrations, consent/rejection, callback URLs, large
  identity IDs and reduced permission responses. Discord PKCE and Instagram
  response/permission behavior particularly need live confirmation; provider mocks
  are not evidence of support by a remote service.
- Complete provider token lifecycle reconciliation, including Instagram long-lived
  exchange/renewal and expiry behavior when a provider omits `expires_in`. Such
  responses currently retain Go's null-expiry storage behavior.
- Handle a provider token being issued or rotated while the final database commit
  is lost or its credential fence rejects the result. No cross-system atomicity
  or automatic callback replay is claimed; fresh consent may be necessary.
- Verify mixed load, proxy routing, secret rotation, callback observability and
  the ownership/rollback rehearsal alongside the rest of the server migration.
