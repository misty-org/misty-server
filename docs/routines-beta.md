# Saved routines: current implementation

The shared SDK describes immutable routine definitions, explicit provider and target
versions, bounded data references and conditions, and capability, agent and wait
steps. `defineMistyRoutine` validates a draft locally; it grants no authority.

The implementation now connects manual backend capability routines and bounded
agent steps through the same execution path. Agent-step admission requires the
separate `MISTY_ROUTINE_AGENTS_ENABLED` flag. Timed waits are connected behind
`MISTY_ROUTINE_WAITS_ENABLED`. Browser/native/view providers, standing grants,
automatic triggers and the Agents authoring UI remain
follow-up work. Defining a schedule or app event does not install a trigger.

## Bounded agent runtime and Go checkpoint controls

The runtime's bounded agent loop (`src/routine-agent.ts`) presents only the step's
pinned tools, runs one model turn per durable stream, carries the complete
transcript across turns and caps a step at 40 capability calls. Queued calls stop
on an unconfirmed/partial result or stream interruption. Generated content is
excluded from usage checkpoints and telemetry.

Admission pins every action/provider/target version, an immutable call namespace
and the selected model. It commits agent checkpoints with the existing run and
dispatch intent. The SDK execution envelope carries these same bindings; model
call IDs are qualified by the admitted namespace. Recovery cannot assign a new
identity to an already pending call. The coordinator preflights the full tool set
before earlier steps execute.

The signed `routine-agent` worker endpoint opens the current step, derives its
prompt from protected prior results and rechecks provider availability. Each model
start consumes an idempotent turn claim under the run lock, enforces the step and
run limits, refreshes the persisted deadline and reserves account/Space quota.
Usage is stored before acknowledgement and settled with a per-node key. A late
usage receipt can be recorded after cancellation; unavailable counts retain the
existing estimated-usage/release policy. Full interrupted/delegated cost
reconciliation remains a release validation item.

Go admits only tools pinned to the active agent step. Matching call/input retries
reuse their original mapping and effect. A new mapping cannot enter while waiting
for approval or after checkpoint completion. The shared effect claim rechecks the
checkpoint under the run lock, closing the gap between scope admission and actual
dispatch. A closed step can only replay an already confirmed effect.

Finishing a step checks the declared output schema, completed model receipts and
all mapped effect receipts. A model's reported success cannot override missing,
partial or uncertain effects. Protected output is sealed with the checkpoint;
only a confirmed output can feed later steps. Whole-run completion counts the
agent's actual SDK effects, not a synthetic step effect. Worker-stop and cancellation
recovery seal unfinished checkpoints conservatively, retain confirmed work and
stop dependent steps without restarting the original prompt.

Runtime workers negotiate both routine protocol 1 and routine-agent protocol 1.
Older workers fail before effects. The runtime build, typechecking, 25 focused
runtime tests and Go evidence tests pass. Fixtures cover model/tool isolation,
transcript continuation, output acknowledgement, lost responses and dependent-step
stopping. New database tests cover pinned admission/replay, model receipts,
checkpoint closure and late-effect dispatch; they compile but have not run.
These checks do not establish deployed model/database compatibility.

## Durable timed waits

Each admitted wait step has a fixed, owner/run/step-bound identity. The signed
`routine-wait` endpoint resolves its timestamp from the immutable program and
protected prior results. Opening a future wait atomically records its deadline,
24-hour expiry, `awaiting_timer` run state and a durable status event. The existing
execution-clock trigger pauses active-time accounting in that same transaction.
A deadline already in the past completes immediately.

The pinned workflow uses the engine's durable sleep with Go's relative remaining
time. Sleeping does not authorize continuation: Go verifies the original wait ID,
current provider/target access, cancellation and its own clock before committing
the wake. An early wake stays waiting, and a duplicate callback for an earlier wait
cannot clear a later one. The next operation restarts the persisted active clock.
Completed waits contribute no synthetic external effect to the journal.

Expired waits enqueue runtime interruption in the same transaction as expiry;
normal cancellation and recovery retain confirmed/uncertain effects. Expiry is
reported as a failed or partially completed routine, not a successful wake.
The public SDK exposes the active wait's identity, step, timestamp and expiry.
Host AI state handling recognizes timer waits and resumes. Workers must negotiate
routine-wait protocol 1 before any routine containing a wait can execute.

The SDK package/packed-consumer checks passed (86 tests), as did focused Go
wait/evidence tests and 19 runtime tests covering the connected wait/coordinator
path. The runtime build and app typechecking also passed. Database tests for
clock pausing, stale callbacks, expiry and interruption
outbox delivery compile but remain unrun. Full host typechecking currently finds
errors in the concurrently changing website-header fixture and file-transfer
surfaces; those files were preserved. This does not establish live wait recovery.

## Trusted controls

All routes below require the authenticated owner through trusted Misty controls.
App-runtime credentials cannot read private routines, save these drafts, start
runs or approve themselves. These routes are deliberately excluded from app RPC.
They are mounted under the existing root, `/api` and `/v1` aliases.

| Method | Route | Purpose |
| --- | --- | --- |
| PUT | `/me/routines/{routineID}` | Save `{ expectedVersion, definition }`; use a UUID and version 0 to create. |
| GET | `/me/routines` | List summaries in an explicit `spaceId` (omit for account scope); supports `limit` and `cursor`. |
| GET | `/me/routines/{routineID}` | Read current definition, or an immutable `version`. |
| POST | `/me/routines/{routineID}/runs` | Manually execute `{ requestId, version, trigger? }`; `requestId` is a stable UUID. |
| GET | `/me/routine-runs/{runID}` | Read admission, current run state, cancellation request and confirmed outcome report. |
| POST | `/me/routine-runs/{runID}/cancel` | Request durable cancellation; inspect the resulting run for actual completion state. |

Saving creates a new immutable version and rejects stale edits. Repeating the same
save after a lost response returns the existing version. It never enables the
routine or changes the version of an active run. A routine cannot silently move
between Spaces through an edit. Reads and saves check current Space membership.

Manual execution uses the selected version and the user's current target grants.
It does not create standing permission. Consequential provider actions continue
through the existing exact-command approval controls. An identical admission retry
returns the same run and call IDs; changing its version, routine or trigger conflicts.
Only one run of a routine may be active. A routine with an uncertain outcome blocks
another admission until that original uncertainty is resolved; a user recovery UI
for that case remains to be implemented.

## Runtime and recovery

Admission commits the existing AI invocation, dispatch outbox, exact capability
pins, input envelope and budget together. It does not create a second run identity
or use the legacy graph executor. The TypeScript coordinator obtains its program
from Go's authenticated context endpoint, negotiates routine protocol 1 and uses
the existing Misty harness capability path. Old workers fail explicitly instead
of interpreting the routine as an ordinary prompt.

Go independently resolves each step's input from the trigger and protected,
confirmed prior results. It rejects out-of-order calls, changed call IDs, changed
inputs and unapproved targets. String reference segments select object keys;
numeric segments select array indexes. Missing references fail; a condition may
explicitly test whether a result exists. Partial results stop dependent work unless
the step explicitly allows partial results. This still produces a partial outcome.

All effects use the existing encrypted SDK effect journal and original effect-ID
algorithm. Confirmed calls replay their original result. Routine runtime checkpoints
contain only metadata; durable `routine.step` events describe journal-observed
progress. Go derives final completion from effect evidence, not runtime text. A
stopped worker is reconciled against the same journal without restarting its prompt.

Cancellation blocks further claims under the same run lock, delivers interruption
through the existing outbox and retains confirmed or uncertain effects. A queued
run without an activated runtime can be cancelled immediately. An active effect
cannot be reported as cancelled while its result remains uncertain. Recovery can
settle existing effects after Space access is revoked, without restoring tool access.

## Deployment and validation

Apply the additive migrations `20270126000000_routine_drafts.sql` and
`20270127000000_routine_manual_runs.sql`, followed by
`20270128000000_routine_agent_steps.sql` and
`20270129000000_routine_timed_waits.sql`, before deploying the updated Go service.
The shared effect-claim guards use these tables, including for other SDK runs.
Deploy a runtime supporting routine protocol 1, routine-agent protocol 1 and
routine-wait protocol 1 before enabling their corresponding admissions. Keep
previously pinned workers available for existing runs.

- `MISTY_ROUTINE_DRAFTS_ENABLED=true` permits draft writes.
- `MISTY_ROUTINES_ENABLED=true`, the existing SDK execution gate and an available
  runtime permit new manual admissions.
- `MISTY_ROUTINE_AGENTS_ENABLED=true` additionally permits manual routines that
  contain bounded agent steps. It does not enable schedules or standing grants.
- `MISTY_ROUTINE_WAITS_ENABLED=true` additionally permits durable timed-wait steps.
  This is separate from scheduled routine triggers, which remain unimplemented.
- Disabling routine admissions preserves draft reads, history, cancellation and
  reconciliation. The broader SDK execution switch retains its existing semantics.

SDK package checks and focused Go/runtime checks pass. Host and app consumers use
synchronized public SDK archives. Shared generated fixtures check Go acceptance
and normalization against the SDK schema. Focused evidence tests cover data
references, lost responses, partial results and uncertain outcomes. Database tests
for draft concurrency, admission, dispatch, revocation and cancellation compile but
have not run because the disposable local database environment is unavailable.
No production migrations, runtime deployment, real-service proof or pilot has been
performed by this implementation work. Keep these flags off for release until the
database/MCP integration gates and macOS scenarios pass.
