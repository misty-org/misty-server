# Native Calendar sources and explicit synchronization

The private Planner Calendar module serves source list/create/delete, available
Google calendars and explicit synchronization, under bare, `/api` and `/v1`
aliases and the existing five SDK methods. This raises native HTTP handler
coverage to 49/74. Contracts remain SDK198/74 and application schema is168.

Source listing includes the synthetic Misty source and retains disabled sources.
Creation resolves the requested Google calendar against the account's available
calendars, excludes free-busy-only entries and defaults display name/timezone from
provider metadata. Re-publishing keeps the existing source ID. Publishing/disabling
and their Space event commit together. Creation returns the initial pending DTO,
as Go does, after attempting its initial import; subsequent listing exposes health.
Disabling clears local watch/cursor metadata and prevents later imports from
reactivating that source. Existing mirrored events remain visible, matching Go.

Source reads require tasks.view; publication, removal and available-calendar
lookup require integrations.manage. App scope ceilings remain calendar.read/write;
available-calendar lookup also needs connections.read, and explicit sync also
needs tasks.write. Credential access retains the legacy owner/Space-owner/shared
resource rule and active account/membership checks. No credential, sync token,
watch token hash or internal revision appears in public DTOs or Space events.

## Provider and persistence behavior

Calendar sources refer to `space_integrations` and `space_provider_credentials`.
They do not refer to personal `connected_accounts` used by Mail. The private legacy
token broker uses the existing AES-GCM provider AAD, including the old Google
Calendar metadata rename. Refresh preserves the old refresh token when omitted,
re-encrypts under the canonical Google AAD and commits before returning the lease.
It reuses the existing bounded OAuth transport and configured Google client.

The fixed-origin Google client encodes calendar IDs as single path segments,
refuses redirects and bounds each response to 4 MiB and each request to 30 seconds.
Available calendars use up to20 pages; events use up to100 pages with a two-minute
operation deadline. Provider errors are returned as fixed codes without raw bodies.

Event pages are fetched outside database transactions. Each page transaction
rechecks the requesting actor, source revision, active connection and exact
credential fingerprint before applying data. Source revision uses PostgreSQL text
precision, not JavaScript's millisecond Date precision. Concurrent source changes
invalidate the import attempt. Page mutations and incremental workflow claims
commit atomically; the final page advances the sync cursor. Failed sync attempts
record source health while preserving the cursor needed for retry.

The import follows Google's [incremental synchronization protocol](https://developers.google.com/workspace/calendar/api/guides/sync):
page tokens retain the original sync cursor; a final sync token is required;
410 invalidates the old mirror and restarts full synchronization. Timed events
retain their offsets; all-day dates use the source/start timezone, with PostgreSQL
performing timezone conversion. Canceled entries without times remove their
existing mirror row. Incremental events create consented, member-owned native
workflow claims with durable inputs and stable typed-event fingerprints. Those
claims are pending execution by the still-incomplete native Agent dispatcher.

## Verification and remaining work

Eighteen restricted-role Planner HTTP/RPC tests pass, including four new source
cases: import/refresh/cancel/disable with SDK response parsing, encrypted token
rotation, cursor expiration and workflow replay, scope/permission denial, and
source disable during provider I/O. Six affected credential unit tests pass.
Typecheck/build pass. Provider requests used a controlled fixture; no live Google
request, schema migration, full-suite/image rerun or deployment occurred.

## Watch callbacks and background reconciliation

Watch creation/renewal and periodic reconciliation are now implemented. The worker
uses the same import service as explicit synchronization. Google watch requests
use the configured public HTTPS API base and request six days of notifications;
a missing watch/resource binding or expiration within24 hours triggers renewal.
The provider's returned expiration is persisted. Replaced/disabled watches are no
longer accepted locally; their remote registrations expire as in the Go behavior.

Before calling Google, the source stores the new channel ID, token hash and
requested expiration. This permits Google's initial sync notification to arrive
before its watch response, as described in the
[push notification protocol](https://developers.google.com/workspace/calendar/api/guides/push).
The existing `/provider-callbacks/google/calendar` route and `/api`/`/v1` aliases
check channel, constant-time token hash, resource identity once bound, expiration,
source/integration status and active owner membership. They atomically increment
a private requested generation, then return204. No detached in-memory task is
needed to retain the notification across a process restart.

Migration168 adds private source ownership, generation, availability and lease
columns. Source DTOs omit all of them. Each import claims a three-minute UUID lease
and has a two-minute operation deadline. Page commits check the current revision,
lease identity and unexpired lease; an expired worker cannot commit its page.
Completion acknowledges only its originally claimed generation. A callback during
import therefore leaves immediate follow-up work. Normal polling is15 minutes;
failed work retries after30 seconds. Active account/Space/member and integration
eligibility are checked before background claims, followed by normal repository
permissions and exact credential checks.

Set `MISTY_NATIVE_CALENDAR_JOBS=1` to enable startup polling. Encryption is required.
When the validated MISTY_PUBLIC_API_URL uses HTTPS, the service registers watches
at its configured API prefix. Without a public HTTPS URL (for example a local
self-host installation), it uses periodic polling without registering unusable
HTTP watches. Shutdown aborts provider requests and waits for the worker before
closing the database. Callback intake remains durable while polling is disabled.

New native sources are marked Hono-owned. Existing sources remain Go-owned after
migration; native claims skip them, and re-publication returns409 rather than
silently adopting them. Go scheduler selection and callback lookup now exclude
Hono-owned sources. Old deployed binaries do not gain that protection until
updated or drained. Explicit legacy source handover, including deployment/rollback
ordering, remains R10 implementation work. No live ownership change has occurred.

Verification at this checkpoint: all21 affected Planner database tests pass,
including3 new cases covering early callbacks, generations, renewal, invalid
callback identity, disable, competing workers, lease expiration and Go exclusion.
Two migration-safety tests and one focused Go scheduler/callback exclusion test
pass; Go uses the separate disposable database. Typecheck/build pass. No public
contract change, image/full-suite rerun, live Google call or deployment occurred.

Integration creation/binding, other provider lifecycles, native Agent execution
and final legacy handover remain open. Go remains deployed; native readiness is
still false. This is not a claim that R3/R6/R10 or production verification is complete.
