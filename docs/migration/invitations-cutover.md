# Native Space invitations

The private API implements owner list/create/resend/revoke, account acceptance
or decline by invitation ID, public token preview and authenticated token response
under bare, `/api` and `/v1` aliases. These are account routes, outside the public
SDK. Responses preserve the existing invitation fields and error codes. Delivery
now returns `pending` after durable enqueue; it no longer waits for Mailjet inside
the HTTP request. Existing clients must tolerate that documented delivery state.

## Authorization and atomicity

Issuance checks the active Space/account and current owner under locks. Invitations
for existing members conflict. A new invitation supersedes previous pending links
for the same Space/email. Resend rotates the generation and token, renews the
seven-day expiry and resets delivery in the same transaction. Revoke and decline
invalidate the link. Public preview hides expired/consumed/revoked links and links
whose Space or inviter is inactive. Responses use no-store and no-referrer headers.

Acceptance checks the authenticated account's current email and the current token
after locking the Space and invitation. Membership insertion, consumption and the
member-joined event commit together. Concurrent token/ID acceptance consumes the
invitation once. Signup after issuance is supported. Previously issued Go token
hashes remain redeemable without the new delivery keys; no plaintext legacy token
is needed. Space-owned notes remain in the Space when membership changes.

## Configuration and delivery ownership

`MISTY_INVITATION_TOKEN_KEYS_FILE` names a private JSON file containing `active`
and `keys`, an array of `{id,key}` entries. Each key is exactly 32 random bytes
encoded as canonical base64. The loader permits at most ten unique keys and a
16 KiB file. Retain old keys while their live pending delivery jobs can still run.
The keyring is separate from password recovery keys and must never enter the SDK.

`MISTY_INVITATION_URL_BASE` retains the existing default
`https://mistysys.com/invite`. HTTPS is required, except explicit self-hosted
loopback HTTP. Credentials, query strings and fragments are rejected. Existing
Mailjet configuration supplies the bounded email transport. Missing delivery
configuration makes new issuance/resend unavailable; reads, revocation and
legacy-link responses remain available.

Migration 153 creates the service-only, forced-RLS delivery table. Rows contain an
invitation ID, token key ID and generation, never a redeemable plaintext token.
The worker reconstructs a domain-separated HMAC token and verifies its stored hash
before sending. Claims have bounded batches, 60-second leases and generation/lease
fences. Failed delivery retries with capped exponential backoff. Revoked, consumed,
expired or inactive-Space jobs become superseded without sending. Expired invitation
cleanup also runs in bounded batches.

`MISTY_NATIVE_INVITATION_JOBS=1` starts this worker only when both invitation and
Mailjet configuration exist. It defaults off. Go remains the deployed owner;
do not enable both implementations as delivery owners during cutover.

Email is at-least-once: an ambiguous provider response or crash after sending can
produce a duplicate message. An in-flight old email cannot be recalled after
resend, but its link is invalid and its stale acknowledgement cannot overwrite
the new generation's delivery state. No claim of exactly-once email is made.

## Evidence and release gates

Two unit tests cover key rotation, token binding, input bounds and URL policy.
Five restricted-role PostgreSQL HTTP tests cover owner/account separation,
single-use races, resend during a blocked send, duplicate workers, delivery
failure/backoff, revoke/expiry, signup after issuance, existing Go links and
transaction rollback when job/event insertion fails. These use fake senders;
no real invitation emails were sent. The complete native suite passed 173 database
tests after all 153 migrations, including the invitation error-code parity fixes
and new membership/ownership lifecycle checks. The unit/typecheck/build check
passes 146 unit tests.

Before switching ownership, provision runtime grants and private keys, verify
Mailjet delivery and website acceptance behavior, rehearse queue/secret rotation
and rollback, and complete issuance abuse controls and larger-data load checks.
Rollback must preserve invitation rows and their token hashes; do not drop the
new delivery table while pending jobs require it. The overall server migration
and production readiness remain incomplete.
