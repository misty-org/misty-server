# Misty lifecycle cancellation and runtime receipts

Account deletion now calls `cancelAccountAi` inside its existing Space/account
transaction. The affected-Space inventory includes AI invocation and device
context Spaces. Cancellation disables account AI, companion and memory settings,
proactive surface preferences and recap schedules; it cancels queued/running/
awaiting-approval invocations, detaches contexts and rejects proposed artifacts.

Completed results stay intact. Runtime bindings, heartbeat/request evidence,
running recap lease timestamps and prior results are retained. Applying artifacts
and started toolbox actions are not rewritten as successful or safely canceled:
purge continues to wait for their reconciliation. A running recap becomes failed
with the owned `account_disabled` code, enabled becomes false and next-run time
is cleared. Its old completion predicate cannot acknowledge that failed row.

The native `createMistyInvocationRepository` adds the runtime event persistence
boundary. It locks Space, active account and enabled AI settings before the
invocation, validates the exact runtime kind/run ID and expiry, and refuses a
changed parent Space. Events are sequential and state changes commit atomically.
After account disable, even a previously valid runtime cannot append an event or
revive a canceled invocation through this repository.

Migration 164 adds a private resulting-state receipt to invocation events. An
exact retry matches event type, JSON payload and resulting state; a conflicting
sequence cannot overwrite committed output. Existing Go events retain NULL and
are not treated as verified native receipts. The legacy public projection selects
explicit event columns and does not expose the new private receipt column.

This repository is not a signed HTTP callback endpoint. The future native adapter
must authenticate runtime signatures and derive identity before invoking it.
Legacy Go event updates do not consistently check account lifecycle; incompatible
writers must be drained at handover. Native invocation creation, activation,
scheduled dispatch, external effects and metering remain part of R4/R6/R7.
Cancellation here means denied future authorization, not proof that an already
dispatched external operation physically stopped.

Verification: 43 targeted PostgreSQL cases passed across account deletion, local
cleanup, both retention phases, native callbacks and migration safety. New cases
cover broad AI cancellation with another-account controls, retained applying
artifacts/recap evidence, disabled schedule selection, ordered callback commit,
exact/conflicting replay, identity and expiry, actual account-lock serialization,
late completion rejection and rollback. Typecheck/build passed. No live runtime,
provider call, deployment, full-suite rerun or image refresh occurred.
