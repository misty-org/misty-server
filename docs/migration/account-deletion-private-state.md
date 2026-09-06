# Private account-state retention phase

This R2 phase is implemented and tested but remains unmounted with account purge.
It does not anonymize the account or mark overall deletion complete.

The current database inventory in `account-data-ownership.json` records 183 direct
user foreign-key or `user_id`/`owner_user_id` references through migration 163.
It includes composite ownership columns with no direct user FK. The categories
are 16 private account-state references, 25 agent-phase references, 15 financial
identity references, 51 shared attribution references and 76 references requiring
another cleanup policy. Shared/financial labels identify attribution to preserve;
they do not authorize retaining all private payloads in those tables. It is not a
complete transitive dependency, email-identity or arbitrary JSON-content inventory.
Unresolved categories are required work, not optional deferrals or completion
evidence. No blanket delete is generated from this file.

`privateAccountTables` explicitly contains recap settings/results, invocation
contexts, conversation focus/pending actions, home activity, private inbox/read/
view/dismissal history, realtime/resolve/reauthentication tickets and account auth
credentials. Deletions always select the exact user. Provider/MCP records, App
data and jobs, device records, Library/search processing and shared content remain
owned by their respective cleanup/reconciliation modules.

Both account-state and AI/agent phases use `runPurgePhase`: sorted Space locks,
then pending account/license, scheduled native request, elapsed retention,
completed payments/providers/local steps and the current unexpired purge lease.
The Space inventory is rechecked after locking the account. A final wall-clock
lease check commits all erasure and the phase receipt atomically, or rolls it all
back. Prior phase receipts survive merging. The purge step returns to pending;
the account remains pending and its request scheduled. Deadlines remain 25
seconds overall, two seconds for locks and five seconds per statement.

Recaps that are running or retain a future lease, and nonterminal AI invocations,
block erasure. Native scheduler/service writers must serialize on account
lifecycle. This prerequisite is explicit: the phase cannot resolve an uncertain
execution merely by deleting its evidence.

Five new restricted-role cases passed with populated recap, home history,
realtime ticket and session fixtures: exact-user isolation; preserved active
shared Space and license identity; refusal of unresolved recap execution;
retention/prerequisite/owner checks; actual lease expiration after earlier erasure
and whole-transaction rollback; old-worker rejection and preserved prior phase
receipts; real Space lock timeout and retry. The seven existing AI/agent purge
cases also passed after extracting the shared transaction helper. Strict
TypeScript and build passed. The restricted account-state test role cannot delete
licenses or read the payments schema. These 12 targeted tests are not a new
full-suite or production-image verification claim.

Next within R2: stop AI invocation/recap schedules at account disable, with durable
execution fences and unresolved side effects preserved for reconciliation. Then
complete the remaining module policies, owned-Space/App/object coordination,
legacy handover and final anonymization. Full release verification remains R11.
