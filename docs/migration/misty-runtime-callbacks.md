# Native Misty runtime callbacks

`modules/misty/runtime-routes.ts` implements the existing POST paths
`/internal/agent-runtime/runs/:runID/activate` and `/events` for native Misty
invocations. It uses inline typed Hono handlers and an injected repository, in
line with [Hono's routing guidance](https://hono.dev/docs/guides/best-practices).
The separate Space-agent run protocol is still a missing migration path.

The wire signature remains HMAC-SHA256 over uppercase method, escaped URL path,
timestamp and the SHA256 of exact request bytes, separated by newlines. Current
and previous secrets are supported, with the existing five-minute skew. The
actual workflow signer is used by the HTTP tests. Bodies are streamed up to the
existing two-MiB bound, with a ten-second read deadline and eight in-flight
requests. Missing signatures, altered bodies/paths and malformed signatures do
not reach persistence. Secrets must contain at least 32 decoded bytes.

Migration 165 adds explicit `runtime_owner` and a service-only receipt table.
Existing rows default to Go; native admission writes Hono. The callback derives
the account from that row and checks account/settings, Space membership, private
conversation, expiry and runtime binding before mutations. It never trusts a
user ID supplied in the body. Receipts cascade with invocation cleanup and store
hashes rather than copies of private node inputs.

Activation binds once and records `invocation.started`/`assistant.status` using
the existing string event IDs. Node callbacks project `tool.started`,
`tool.completed`, `tool.failed`, status text and model `response.delta` as Go does.
Node state is deliberately separate from invocation state: node completion is
not invocation completion. Stream rows, receipt and heartbeat commit together.

The workflow's idempotency header is required but is not signed. Effect identity
therefore uses signed node ID/state (or activation), with the exact body hash for
conflict detection. Exact retries, including changed header keys, do not emit a
second copy. A changed body under the same node/state returns 409; changing JSON
serialization also changes that hash. The current workflow serializes the same
payload deterministically on a step retry.

Model-start events require an injected idempotent usage-admission operation. It
runs outside the callback transaction to preserve the usage repository's lock
order. Account/runtime access is rechecked afterward. The future concrete adapter
must use the invocation's stable reservation key across model nodes and retries.
These endpoints alone do not dispatch models, finish invocations or settle usage.

The factory can be supplied as `createApi({ runtimeCallbacks: ... })`; production
main deliberately has no callback dependency yet. Public admission, dispatch,
the concrete pricing/usage adapter, context/tools, `/complete`, streaming reads
and Space-agent callbacks are remaining R4 assembly work. Existing live Go is
unchanged. Ownership routing/draining is required before sharing traffic between
old and new processes; the ownership column alone does not retrofit a live Go
binary.

Focused verification passed: two HTTP tests, three real PostgreSQL callback-route
cases and twelve admission/persistence regressions, plus typecheck/build. They
cover the actual signer, key rotation, tampering/request bounds, native ownership,
activation and concurrent replay, stream DTOs, node/ invocation state distinction,
usage dependency and disabled-account denial. No live model/provider execution or
production-readiness claim is inferred from these local results.
