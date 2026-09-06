# Provider cleanup for account deletion

Status: private credential readers, bounded provider-effect adapters and a durable
provider-stage worker factory are implemented and tested. Dropbox refresh/revocation
has durable recovery; other credential refresh,
provider-specific subscription cleanup, remaining provider policies and complete
lifecycle composition are unfinished. The worker is not mounted by the production
entrypoint and does not enable public account deletion.

## Existing data and compatibility

The Go lifecycle path enumerates `cloud_connections` and
`space_provider_credentials`. Newer `connected_accounts`, Figma bindings/webhooks
and other integration dependencies also require cleanup. The native migration
must include both generations rather than considering the older query complete.

Connected accounts use AES-256-GCM with `misty-connected-account-v1:<provider>`
authenticated data. Legacy cloud/Space credentials use
`misty-provider-v2:<provider>` with a nested `Token` envelope or the older flat
token representation. Go's Google Calendar metadata migration also retains the
`misty-provider-v2:google_calendar` read fallback for provider `google`. The native
cipher now implements exactly that fallback; it does not try other providers or
cross the connected/legacy format boundary.

Both readers require key version 1, a 12-byte nonce, authenticated ciphertext and
the existing 2 MiB plaintext-envelope bound. The deletion decoder validates token
types and header-breaking characters, preserves legacy custom OAuth client
identity, rejects ambiguous nested/flat envelopes and clears the decrypted byte
buffer before remote I/O. Stored encrypted bytes are unchanged. Missing keys,
invalid ciphertext or unreadable tokens raise a module-owned error: these are not
evidence that external credentials have been revoked. Five shared fixtures are
opened and resealed by Go and read by TypeScript, including the Calendar rename,
flat/nested tokens and custom-client envelope.

## Bounded remote effects

The gateway performs one effect at a fixed provider origin. It allows four active
calls, applies a ten-second deadline shared with caller cancellation, follows no
redirects and performs no automatic retry. Parsed responses are limited to 16 KiB
with strict UTF-8, declared-length checks and stream cancellation. Provider text
and transport diagnostics never become persisted/user-visible errors.

| Effect | Verified receipt and recovery boundary |
| --- | --- |
| Google token revocation | HTTP 200 confirms revocation. HTTP 400 with `invalid_token` records the selected token as inactive; other errors retain retry work. The caller must prefer a retained refresh token. |
| Dropbox access-token revocation | HTTP 200 confirms revocation of that access token and its corresponding refresh-token family. The worker derives and checkpoints an access token from the retained refresh token first; HTTP 401 alone never completes cleanup. |
| Discord token revocation | Uses form encoding, authenticated OAuth client credentials and an explicit access/refresh hint. HTTP 200 confirms revocation. |
| Figma webhook deletion | HTTP 200 must contain the exact requested webhook ID. HTTP 404 remains ambiguous because it also covers insufficient permissions. |

Google documents inactive-token and malformed-request errors separately in its
[revocation reference](https://developers.google.com/identity/openid-connect/reference#revocation_endpoint).
Dropbox documents token revocation and refresh behavior in its
[getting-started reference](https://www.dropbox.com/developers/reference/getting-started)
and [OAuth guide](https://developers.dropbox.com/oauth-guide).
Its authoritative [API specification](https://github.com/dropbox/dropbox-api-spec/blob/main/auth.stone)
states that revocation includes the corresponding refresh token and other access
tokens issued from it.
Discord's [OAuth reference](https://docs.discord.com/developers/topics/oauth2#token-revocation-example)
specifies form data and client authentication; revocation covers the authorization's
associated access and refresh tokens.
Figma's [webhook endpoint reference](https://developers.figma.com/docs/rest-api/webhooks-endpoints/#delete-webhook)
defines the deletion object and ambiguous 404 response.

The gateway returns evidence only. The durable worker owns credential erasure and
resource/stage acknowledgement after account/resource identity and lease
verification. Revoking a user authorization
does not imply uninstalling a shared organization bot or deleting shared content.

## Durable resources and acknowledgement

Application migration 159 adds `account_deletion_provider_resources`, protected by
service-only forced RLS. The account/request-bound queue records current connected
accounts, legacy cloud and Space credentials, Figma webhooks and provider
subscriptions. Credential resources retain encrypted source bytes and their format;
dependent resources reference that credential's kind/ID. Nonsecret resource identity,
source fingerprints, retry timing and completed outcomes survive restart. Completed
credential resources must have null ciphertext/nonce. The forward-only rollback
retains unfinished work and evidence.

The worker claims the existing providers step with its two-minute lease and handles
one resource per iteration. Preparation and acknowledgement lock affected Spaces
in order, then the pending-deletion account with its exact license, native request,
step lease and source resource. SQL lock/statement deadlines are two/five seconds.
No SQL lock spans provider I/O, which has the worker's 45-second operation deadline
and the gateway's ten-second deadline. Source fingerprints and lease validity are
checked again before erasure. The final step update rechecks database wall time;
expiry rolls back source erasure and resource acknowledgement together.

Webhooks/subscriptions precede their credential resource. Failures retain encrypted
retry bytes, record owned error codes and back off per resource from 15 seconds to
one hour. Independent resources can continue while a failed resource waits. A stage
with no currently eligible resource polls after 30 seconds; it cannot complete while
any resource is pending. Source replacement, conflicting subscription ownership or
an erased credential reappearing blocks completion rather than silently adopting
new bytes. Missing source records currently require reconciliation as well.

Implemented effects cover Google/Drive token revocation, Discord revocation,
Dropbox access/refresh credentials, Figma webhook deletion and explicit local erasure
for Microsoft/OneDrive/Figma delegated credentials. Invalid ciphertext still blocks
these local outcomes. Provider subscriptions, missing/ambiguous OAuth configuration
and unsupported providers remain pending for their missing handlers or resolution.
An old local subscription expiry or disabled flag is not treated as remote evidence.

After every resource completes, a final transaction disables related Figma bindings,
webhooks, integration/shared-resource access and provider subscriptions, removes
the account's provider-event inbox, records provider completion and acknowledges
only the providers step. Membership/shared content and other lifecycle steps remain
for their owners. The runtime role needs the explicit source SELECT/UPDATE and
queue privileges; RLS dependency reads also require SELECT on Space membership.

## Dropbox refresh-token recovery

Migration 160 adds a separate encrypted execution envelope and OAuth client-ID
hash to each provider resource. The original source snapshot stays immutable.
An access-only credential can be revoked directly. When a refresh token exists,
the worker first requests a new access token from Dropbox using that refresh token
and the configured client, or the original custom client retained in a legacy
cloud envelope. The response must have a valid token type and positive expiry.
The private refresh client has a ten-second deadline, four-operation admission,
16 KiB response bound, no redirects and no automatic retry.

The new token pair is encrypted, then checkpointed under the current account,
request, source fingerprint and unexpired step lease before any revocation request.
The checkpoint retains the client-ID hash as evidence of a successful refresh with
that client. A lease that expires before this commit prevents revocation from
being dispatched by that worker. Completion clears both encrypted credential
copies and their nonces while retaining nonsecret outcome/client evidence.

Retries first attempt revocation with the checkpointed derived access token. If
it is rejected, the worker refreshes again with the retained token pair and exact
same client ID. A rejected refresh can establish an inactive token only after that
earlier successful checkpoint. An initial `invalid_grant`, a changed client ID or
other ambiguous error remains pending; it must not erase credentials merely
because the current OAuth configuration may be wrong. Lost refresh responses can
retry Dropbox's reusable refresh token, and later family revocation covers access
tokens derived from it. This protocol is specific to Dropbox; it is not a general
assumption about rotating refresh tokens at other providers.

## Remaining implementation and release checks

Refresh expired access tokens for the remaining providers
only through a durable credential checkpoint, honoring custom clients and token
rotation. Define explicit outcomes for providers without an applicable remote
revocation action and resolve ambiguous remote absence without claiming success.

Complete account-state gates for provider event ingestion and all remaining provider
writers before activation. Retain shared attribution. Include GitHub/shared-app
ownership and legacy provider subscriptions in the inventory; do not infer their
cleanup from token revocation alone.

Nine unit tests cover credential formats, provider binding, tampering, key version,
malformed tokens, secret-buffer clearing, fixed endpoints, encoding, rejected/
ambiguous responses, response bounds, lost transport outcomes, concurrent limits
and stream cancellation. Ten restricted-role database tests cover both credential
generations, lost revocation responses, dependency ordering, independent progress,
source replacement/reappearance, stale leases, unlocked I/O, Figma local cleanup,
unresolved subscriptions and actual lease expiry during acknowledgement. No real
provider revocation has been performed. Representative inventory/import, real-provider,
process/refresh-loss and complete lifecycle rehearsals remain required before activation.

Six additional database cases verify encrypted Dropbox checkpoint ordering,
lost revocation and refresh replies, original custom-client selection, changed/
unproven client identity and expired checkpoint leases. Four refresh-client unit
tests cover fixed endpoints, form encoding, strict replies, rejected grants,
malformed inputs, concurrent admission and canceled streams.
