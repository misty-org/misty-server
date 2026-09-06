# Private AI and agent retention purge

Implemented as a phase of R2 in `completion-checklist.md`. The repository remains
unmounted with the other incomplete account-deletion composition. It is not a
complete account purge or proof of remote object erasure.

`createDeletionPurgeRepository().purgeAgents()` accepts only a current native
purge claim. It locks affected Spaces in sorted order, then the pending account,
scheduled request and purge lease. It rechecks the account/license identity,
database retention deadline and all three completed prerequisite steps. A changed
Space inventory retries instead of acquiring a new Space after the account lock.
Operation, lock and statement deadlines are 25 seconds, two seconds and five
seconds respectively.

The transaction refuses active related Space runs, AI invocations, agent job
leases/dispatch, device jobs, applying artifacts and unresolved started tool
actions. Upstream cancellation and reconciliation must stop these effects before
erasure; this phase does not invent a successful result for an ambiguous action.
Lifecycle guards on every native service writer and draining incompatible Go
writers remain composition requirements.

Private conversations/events, current Misty memories, AI invocations and their
events, artifacts, feedback, private retrieval documents/chunks, preferences and
cleanup state are removed. Legacy private Space-agent conversations and instances
are removed with their cascaded events/memory/workflow configuration. Attachment
deletion uses migration 163 to preserve both image keys in durable cleanup jobs.

Owned personal agents and their versions keep IDs but lose names, descriptions,
instructions, voice and custom avatar metadata. Agents stay disabled and their
MCP tool assignments are disabled. Their definition snapshots are redacted in
related runs, including runs requested by another member. Other members' run
results remain. Runs owned/requested/initiated/billed by the deleting account lose
private input/result/output/artifact/context payloads; numeric progress, identity,
state and accounting references remain. Private context records are removed and
approval credentials/summaries are cleared. Toolbox audit rows retain their
idempotency keys and terminal state while private request/result/session data is
redacted, preventing accidental replay from erased history.

Inconsistent cross-account attachment/artifact/feedback ownership is rejected
before destructive cascades. Final acknowledgement checks the lease again using
database wall time. Expiration rolls back all data changes and attachment jobs.
The receipt records `agent_data: purged` and `attachment_objects: queued`; the
purge step returns to pending and the account/request stay pending/scheduled.
No final completion or anonymization is performed. Reclaimed execution repeats
idempotent cleanup rather than trusting an old phase receipt over current data.

Seven restricted-role PostgreSQL cases cover actual private-data removal and
other-account isolation, retrieval/event cascades, preserved shared results and
license records, image intents, exact identities, both retention checks,
prerequisite rollback, expired/reclaimed leases, real expiration during SQL,
active-work refusal, inconsistent attachment ownership, legacy owner exclusion,
reactivation and cancellation. Strict TypeScript/build pass. These targeted
results do not replace full release verification or prove every legacy cascade
with populated production data.

Remaining R2 requirements include other private account/provider/MCP state,
owned-Space retention and permanent cleanup, App purge coordination, storage and
collaboration delivery, ambiguous action reconciliation, writer lifecycle guards,
legacy request handover and final anonymization with financial/shared attribution
preserved. Representative large-account SQL/resource bounds and hosted/self-host
end-to-end deletion remain required before activation.
