# Native Misty invocation admission

The native repositories now implement account-session creation, runtime activation
and event persistence. They remain internal dependencies; public creation,
provider dispatch and usage integration are
not yet mounted in production main. Signed HTTP activation/node-event factories
are now implemented; see `misty-runtime-callbacks.md`. These repositories alone do not authorize model/tool spending.

Creation validates the input shape and two-MiB payload bound, locks active Space,
account, enabled AI settings, membership, exact session and optional private
conversation. Conversation ownership and Space binding must match. It inserts a
server-generated `invocation_` ID in queued state with the existing Go lifetime of
24 hours measured by database wall time. The exact session is checked again
before commit so expiry during insertion rolls back the entire creation.

Concurrent identical keys converge on one invocation. Replays compare Space,
conversation, surface, mode, trigger and JSON payload; JSON object key ordering
does not change equality. Replays do not extend lifetime, change state or enqueue
another row. Unlike the legacy Go insert's unconditional reuse, a different body
under the same key raises an explicit idempotency conflict. This is an intentional
compatibility tightening: HTTP integration and client retry proof must verify
that retries retain the original request body rather than silently reusing a key
for another action.

Activation rechecks active account/AI settings, Space membership, conversation
ownership/lifecycle/binding and expiry under the same lock order. A queued row can
bind one runtime kind/run ID. Competing identities cannot take it over; exact
activation replay preserves running or awaiting-approval state. Completed,
canceled, expired or otherwise terminal invocations cannot be activated. The
existing global runtime-run uniqueness constraint remains enforced.

Callbacks now share the same membership and conversation checks. A removed
member or deleted private conversation cannot continue producing results through
the repository. Final state updates recheck invocation expiry after event insert;
expiration rolls the event receipt back. Private resulting-state receipts from
migration 164 continue to distinguish exact transport replay from conflicts.

Twelve targeted restricted-role PostgreSQL tests passed: eight admission cases
and four existing callback cases. They include simultaneous exact creation,
unchanged retry TTL, conflicting payloads, exact-session/account/AI denial,
Space/conversation isolation, competing activation and stable runtime identity,
membership loss, conversation deletion, account cancellation, expiry, real
session expiration during insertion, real invocation expiration during event
insertion, and callback rollback/replay. Typecheck/build passed. No migration,
SDK contract, deployment, provider effect or image change occurred.

Remaining R4/R6/R7 gates: authenticated HTTP adapters, scheduler-specific trusted
admission, exact App capability policy where supported, feature/model/attachment
validation, atomic usage reservation, durable dispatch, runtime context/tool
authorization, reconciliation and complete hosted/self-host client behavior.
Signature verification must derive runtime identity; caller JSON is not identity.
Go remains the deployed owner pending the complete handover and release proof.
