# Native draft provider adapters

The private Gmail and Microsoft Graph draft adapters and native HTTP/RPC routes
implement create, update and send. Native coverage is 35 of 74 public API methods,
including all eight mail methods. Go remains deployed pending the complete server
cutover. The provider and local concurrency checks do not prove production rollout
readiness or replace the remaining external verification below.

## Input and MIME

The shared SDK schema remains the public contract: 10 MiB combined decoded body
and attachment bytes, 28 MiB serialized draft, bounded addresses/header fields and
100 attachments. The private preparation step validates actual mailbox addresses,
rejects header injection and noncanonical base64, and exposes decoded buffers only
to server adapters. Names and filenames never become file paths or fetched URLs.

Pinned Nodemailer MailComposer renders Gmail MIME with file and URL access
disabled. Bcc is explicitly retained because the Gmail API receives the complete
MIME message; Unicode names/subjects, plain text, attachment bytes and inline
content IDs survive parsing with an independent MIME parser in tests. The rendered
message and provider JSON have additional size bounds. Nodemailer is an API-only
production dependency; the test parser is development-only.

Gmail replies fetch bounded metadata and select the latest non-draft parent. They
set valid Message-ID references and In-Reply-To and require a matching reply
subject. The existing Go implementation supplied only threadId. These headers are
required by [Gmail's thread contract](https://developers.google.com/workspace/gmail/api/guides/threads).
Standalone drafts remain editable without inventing a reply parent. Updating and
sending check the existing draft ID and DRAFT label; sending supplies only that ID.
Minimal successful Gmail message responses may omit payload, so the shared native
normalizer now correctly accepts that shape.

## Graph lifecycle and attachments

Replies use the latest existing non-draft message's createReply endpoint and then
apply the caller's draft fields. Requested conversation identities must match
exactly. Updates and sends require isDraft=true. Empty recipient arrays explicitly
clear removed recipients, unlike the old omitempty behavior. Attachment replacement
collects every bounded page before deleting anything, then applies the requested
set. Each provider write goes through the supplied current-access guard.

Small attachments use the fileAttachment endpoint. Attachments at or above
3,000,000 bytes use createUploadSession and ordered 2 MiB byte ranges. This keeps
the public 10 MiB limit usable for Outlook as well as Gmail. Session URLs must be
HTTPS on outlook.office.com with the documented attachment-session path, without
userinfo or fragments. Their tokens never enter logs or public responses, and the
OAuth Authorization header is never forwarded to these preauthenticated URLs.
Each upload checks expiry, cancellation, a 15-second deadline, a 256 KiB response
limit, exact next-byte progress and final 201 completion. Redirects and retries
are disabled. This follows Microsoft's [large attachment protocol](https://learn.microsoft.com/en-us/graph/outlook-large-attachments).

Gmail and Graph creation expose a private callback after obtaining the draft ID and before
any subsequent mutation. The service persists the target in its audit intent; failure stops further
attachment/reply updates. A provider-created draft can
still exist when its response or subsequent SQL commit is lost. These operations
are not an atomic transaction across Microsoft and PostgreSQL.

## Verification and remaining integration

Thirteen provider tests cover independent MIME parsing, Bcc/Unicode/inline metadata,
invalid headers/addresses/base64/body bounds, Gmail reply and standalone updates,
exact-ID sends, Graph reply selection with timestamp precision, recipient clearing,
attachment pagination, small/large uploads with actual bytes, unsafe/expired session
rejection, unexpected ranges, revocation, audit-callback failure and no retries.
The full unit suite passes 154 tests. All providers in these tests are local fakes;
no real draft or email has been created or sent.

The native routes share two large-body permits across REST and SDK RPC aliases.
Authenticated mail.write RPC candidates acquire a permit before buffering the
larger envelope; excess work receives 503 without reading the request stream.
Trusted nested dispatch reuses its active permit without a caller-controlled
header. Permits release on completion/error, and detached work cannot reuse an
expired permit. Other RPC/record envelopes retain their 4 MiB bounds.

Draft operations have a 90-second total deadline and each provider request has a
15-second deadline. Existing draft edits/sends acquire a PostgreSQL session
advisory lock keyed by connection and exact provider draft ID. The operation
reserves one connection and reuses it for every audit/current-access transaction,
so even a pool of size one cannot deadlock waiting for another connection. A busy
draft returns 409. Losing the lock connection aborts current work; uncertain locks
cause the connection to be discarded rather than reused. These session locks need
direct PostgreSQL connections or session pooling, not transaction pooling.

The audit intent commits before provider access. Known newly created draft IDs
are persisted before subsequent provider writes. A definitive refusal before any
successful mutation records its sanitized error; partial/ambiguous writes record
mail_operation_incomplete. Unfinished/incomplete history or a previously successful
send blocks subsequent edits/sends with mail_draft_reconciliation_required. The
index in migration 155 bounds this lookup. No automatic replay or blind resend is
performed. Audit completion failures retain unfinished intent.

Public RPC requires authoring_source=user/ai and confirmed:true. Direct App-session
REST also requires explicit confirmation; account-session REST preserves the
existing effective confirmation for user-authored sends. AI-authored sends always
need explicit confirmation. Denials and successful sends retain the supplied
source and effective confirmation in content-free audit records.

The mail PostgreSQL HTTP/RPC suite has 23 tests. It verifies both provider draft
lifecycles, actual 4 MiB attachment bytes through the RPC/normalizer boundary,
all aliases and opaque IDs, confirmation/provenance, concurrent edit/send denial,
ambiguous-send replay prevention, created-draft identity, actual connection-removal
lock contention, App revocation, SQL/audit failures and loss of the coordination
connection. The full PostgreSQL suite passes 201 tests/23 files/156 migrations.

The release-image memory fixture submits two concurrent maximum-size binary or
escaped-text drafts through each REST/RPC path, with real loopback HTTP and MIME
encoding. A third headers-only request receives 503 before uploading its body.
The same fixture peaked at approximately 599 MiB before removing redundant native
RPC serialization and 455 MiB afterward. All eight accepted requests completed.
Validated draft objects now cross the internal dispatch boundary through a private
Request-identity WeakMap; HTTP headers and cloned requests cannot opt into it.

Run `node scripts/migration/measure-draft-memory.mjs` after building the API smoke
image. [Before](mail-draft-memory-before.json) and [after](mail-draft-memory.json)
reports record exact image digests, payload sizes and cumulative API-process peak
RSS. This is one paired run in a 1 GiB container, with fixture authentication,
database coordination and a local provider. It does not establish a safe 512 MiB
production limit or mixed-workload throughput.

Remaining release gates:

- Provide an operator reconciliation workflow for ambiguous/unfinished drafts and
  sends, with provider-state evidence before resolving a blocked record.
- Verify real Gmail/Graph staging accounts, large upload expiry, provider-side
  concurrent edits, threading and end-to-end delivery behavior.
- Measure mixed-workload memory and throughput with production authentication,
  database coordination and realistic provider latency; the isolated draft fixture
  does not establish deployment capacity.
- Verify native OAuth against production-like provider registrations and complete
  the wider server migration before switching deployed ownership away from Go.
