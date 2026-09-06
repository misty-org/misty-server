# Inbox RPC compatibility and native mail migration

The public SDK now defines eight methods: mail.accounts.list, mail.folders.list,
mail.threads.list/get/action and mail.drafts.create/update/send. The server snapshot
and Go registry agree with that separately published contract. Read methods require
mail.read; mutations require mail.write. App RPC still requires a live installed
app session bound to an active Space, including these account-scoped operations.
Domain handlers retain current-account ownership checks on the selected connection.
The public SDK exports no provider tokens, Stripe or private server code.

## Provider IDs and routing

Only the named mail threadID/draftID parameters permit provider IDs: 1–320 printable
ASCII bytes, no whitespace/controls, and no exact dot or dot-dot identifier. Space
and other Misty identifiers retain their existing strict form. IDs are encoded as
one path segment; plus, slash, padding, percent, question mark and fragment characters
remain data. Literal `%2F` must not become slash through a second decode.

Go forwarding now sets a consistent decoded URL.Path and encoded URL.RawPath.
Mail handlers decode Chi parameters only when RawPath was used for matching.
The previous MailThread handler's path-plus-query double decoding corrupted plus
and percent IDs and is removed. Both direct REST and RPC routes are covered.
Native dispatch uses encodeURIComponent for the segment and URLSearchParams for
queries; actual Hono routing verifies the same opaque values.

## Draft bounds and send confirmation

Ordinary RPC envelopes and personal-record requests remain capped at 4 MiB.
Authenticated mail writers with current Space membership can read an absolute
draft envelope ceiling of 28 MiB plus 64 KiB for the wrapper. After decoding,
only mail.drafts.create/update may exceed the ordinary limit. Go enforces the
28 MiB raw draft-body limit; public/Hono schemas also enforce a 28 MiB serialized
draft and the providers' 10 MiB combined decoded text/attachment limit. Other
methods cannot obtain a larger accepted envelope by holding mail.write.
Provider-side validation remains authoritative for actual MIME delivery limits.

RPC sends require an explicit authoring_source of user or ai and confirmed:true.
This is checked by both the public/Hono schema and Go RPC boundary before dispatch;
existing direct REST confirmation behavior is preserved. Thread mutations accept
the actual read/archived/starred fields, without inventing unsupported actions.

## Verification and remaining work

Go resolver/envelope tests cover narrow ID validation, encoded routing, size and
whitespace bounds, unrelated-method denial, and send confirmation. Actual Go
mail handlers with a database and fake provider verify direct/RPC IDs
`a+b/c==`, `%2F`, `%25?x#y` and `a/../b`, plus a 4 MiB attachment arriving intact at
draft creation. No provider send was invoked in that new end-to-end test. Existing
mail capability/health/confirmation checks and exhaustive 74-method catalog
authorization checks pass. The Go fixture database was upgraded to migration 153
before execution and is separate from the native regression database.

Three RPC unit tests exercise mail capabilities/consent, draft limits and real
Hono parameter decoding. Native account/folder/thread reads now use the private connection
token broker, fixed-origin Gmail/Graph adapters and explicit public response DTOs.
All aliases and the four mail read RPC methods use these native handlers.
The total native RPC coverage is 35 of 74 methods.

Mail reads require mail.read and the connection's mail capability, without adding
an unrelated connections.read requirement. Accounts list includes only the owner's
non-revoked Google/Microsoft mail connections. Profile failures produce the Go-style
needs-attention fallback. Microsoft identities are probed for an actual mailbox;
identities without one remain visible with mail_provider_mailbox_unavailable.
Folder reads preserve provider IDs, normalized kinds, counts, colors and Graph's
immutable-ID preference. Graph pagination reuses only an opaque skip token against
the fixed endpoint, rejects untrusted origins/userinfo/fragments, and caps pages
at 20 and folders at 500. Repeated tokens fail instead of looping.

Provider GETs reject redirects, have a 15-second/request-cancellation deadline,
and share a 16 MiB streamed response budget across each reader operation, including
all metadata requests and pagination. The complete operation has a 45-second
deadline; account probes use at most four concurrent workers and Gmail thread
previews use at most ten. Fatal preview failures cancel sibling requests. Responses contain
only sanitized mail error codes, including 424 for provider authorization failure,
422 for unavailable Microsoft mailboxes and 429 for provider throttling. Domain
access and credential identity are checked again after external reads. An App
uninstalled or connection removed while the provider is responding cannot receive
the pending result. Provider health updates cannot overwrite a rotated credential.

Nine restricted PostgreSQL HTTP/RPC tests cover real native composition, aliases,
DTO parsing, private OAuth refresh, mailbox fallback, grants, ownership, thread
filters, opaque IDs, date precision and in-flight uninstall/removal. Fourteen
provider tests cover normalization, opaque pagination, cycles/page/row limits,
unsafe counts, shared streamed size limits, fatal fanout cancellation and sanitized
errors. Four shared synthetic Gmail/Graph fixtures in `fixtures/mail-threads.json`
are independently checked against the existing Go provider and HTTP DTO using
`go test ./internal/platform/httpapi -run TestNativeMailThreadFixtures -count=1`.
They preserve encoded names, MIME bodies, HTML-to-text conversion, attachment
metadata, participant ordering, zero dates and nanosecond timestamps. Provider
HTML remains untrusted content and requires the existing client rendering boundary.
Gmail malformed/oversized MIME is bounded by part count, depth and decoded body
size. Graph conversations are bounded to 20 pages/500 messages. Only an initial
Graph 400 retries without attachment expansion; authorization/throttling never
become a successful preview. Requested conversation/thread IDs must match exactly.

The full check passes 154 unit tests and 201 PostgreSQL tests across 23 files and
156 migrations. These use fake providers; no real mailbox,
message send or production credential is involved.

## Native thread actions and audit durability

Thread actions now run natively through every REST alias and mail.threads.action.
They require mail.write and the connection's mail capability, including live App
and Space access; mail.read and connections.read are not additional requirements.
Gmail applies explicit label additions/removals in one request. Graph gathers and
validates the complete conversation before any write, with fixed-origin pagination,
20-page/500-row limits, exact conversation IDs and deduplicated message IDs. It
then performs sequential property patches and archive/inbox moves. Empty label
lists remain arrays in the public DTO. Generic missing-item Graph errors return
404; only explicit unavailable-mailbox errors receive the mailbox classification.

The adapter follows the documented [Gmail thread modification API](https://developers.google.com/workspace/gmail/api/reference/rest/v1/users.threads/modify),
[Graph message update API](https://learn.microsoft.com/en-us/graph/api/message-update?view=graph-rest-1.0)
and [Graph move API](https://learn.microsoft.com/en-us/graph/api/message-move?view=graph-rest-1.0).

Migration 154 adds nullable completed_at to the existing audit table. Existing Go
inserts retain a terminal default; native actions first commit an intent with
success=false, mail_operation_pending and no completion timestamp. Audit insertion
failure prevents provider access. Each external write holds current account, App,
Space and connection locks for its bounded request. Revocation waits for that
write, then prevents subsequent writes. Requests do not retry provider mutations.
No subjects, recipients, bodies, attachment names or provider payloads enter audit
records. The runtime role can update outcome/completion fields, not source or owner.

Success is recorded before the final response access check. A failure after a
write attempt records mail_operation_incomplete, since multi-message changes can
be partial and a lost response does not prove the provider rejected the action.
If completion cannot commit or the process crashes, the intent remains unfinished.
Never automatically replay these records: inspect the provider state and reconcile
the outcome. This preserves evidence rather than claiming cross-system atomicity.
A production monitoring/reconciliation workflow remains a release gate.

Five provider action tests cover label mapping, Graph ordering, invalid pagination,
per-write guards, partial failures and no retries. Five additional restricted
PostgreSQL HTTP/RPC tests cover durable intent, legacy inserts, narrow audit grants,
validation/ownership, failure outcomes, actual row-lock contention during removal,
and injected audit insertion/completion failures. The thread read/action subset has
14 HTTP/RPC tests and 19 provider unit tests; the draft implementation below adds
9 PostgreSQL cases and 13 provider tests.

All eight mail RPC methods now have native handlers. Draft create/update/send
use the adapters, durable audit and serialization described in `mail-draft-adapters.md`.
OAuth authorization/callback now have native handlers; see `connection-authorization.md`.
Production failure reconciliation remains incomplete. Go remains deployed. Inbox remains
unpromoted until the SDK task's remaining component/native gates pass. Production
provider verification, bounded memory/load checks for concurrent draft requests
and the full mail lifecycle are required before native ownership can switch.
