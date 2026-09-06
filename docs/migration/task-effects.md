# Native task effect processing

`modules/planner/tasks/effects.ts` consumes the mutation queue introduced in
migration 166. `MISTY_NATIVE_TASK_EFFECTS_JOBS=1` enables its polling worker in
the API composition root, with the existing runtime-role and shutdown handling.
It only creates downstream database work; it does not call a model/provider.

Processing locks the active Space/account and rechecks task management access,
then locks one pending effect. Assignment creation, workflow claims and effect
completion share one transaction. There is no external request inside that
transaction. Concurrent processing or process restart cannot commit two copies
of the same downstream assignment. Failed SQL rolls everything back; the polling
adapter records a sanitized error and delays retry. Unavailable actors cancel
their pending effect. Invalid run/device context remains visible as a pending
effect with its admission error, without a partially authorized run.

Assigned-Agent creation ports the existing `ClaimAssignedAgentTaskRun` behavior:
the current task must retain that assignment, its matching assignment activity
must exist, the creator must own the enabled Agent, and the current immutable
version supplies its instructions/model/run mode. Superseded assignment events
do not queue another old run. The stable task/Agent/assignment-version identity
prevents re-creation. The existing run, job, queued activity and Space event are
written together. Source context is limited to eight owned, non-revoked devices,
known browser/project kinds and the existing capability allowlist. Capabilities
are deduplicated, and run contexts expire after 24 hours.

Task-change workflow fan-out retains the member-instance/workflow-version/event
identity, explicit enabled/consent checks and include-Agent-changes setting. It
checks active member access, agent-run/task-view permission and task audience.
Each eligible claim is `claimed`, with a durable private request for the future
native workflow runner. An existing Go or native claim is never overwritten.
The existing bounded fan-out limit of 200 targets remains. Completion of the
source effect means downstream work was persisted, not that a workflow ran.

Migration 167 marks native `space_runs` and workflow claims with explicit
execution ownership; existing rows remain Go-owned. The Go scheduler's claim,
terminal-job synchronization and stale-run reconciliation now filter Go-owned
runs. A running old binary does not acquire this behavior automatically; drain
or update it before introducing native-owned jobs into a shared environment.
User/account cancellation intentionally continues to cancel owned work across
both implementations.

Verification: five new restricted-role database cases cover concurrent replay,
actual run/job/context creation, revoked access, stale assignment, rollback,
failed device admission and consented workflow identity. Ten affected Planner
route cases also pass. Two focused Go scheduler checks pass on the separate test
database, including actual native-owner exclusion. Typecheck/build pass. Both
owned test databases have migration 167; the SDK fixture was untouched.

Remaining integration: native agent-job dispatch, complete Space-agent runtime
callbacks, concrete workflow capability validation/execution/approval handling,
usage and final completion/reconciliation. These are required R3/R4/R7 work.
The worker flag was not enabled in a running deployment, and no live Agent,
workflow or provider operation occurred.
