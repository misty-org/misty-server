# Native account deletion: local cleanup

The local repository and worker are implemented but remain unmounted. Production
account deletion still requires the remaining provider handlers, retention purge,
legacy worker handover and release rehearsal described in `account-deletion-cutover.md`.

The worker claims only native requests whose payments and providers steps have
completed. A single service transaction locks affected Spaces in ID order, then
the pending account, request and current local step. It rechecks the Space
inventory, account/license identity, both prerequisites and the unexpired lease.
An active owned Space with another member blocks cleanup again, guarding against
a legacy membership writer racing the original deletion request.

For other owners' Spaces, cleanup removes the departing membership and private
conversation membership, cancels that member's runs and schedules, and preserves
collaborative notes, drawings and other shared content. Journal ACL versions and
durable control outboxes invalidate previously issued room tickets; Space events
and realtime notifications accompany the membership change.

Owned active or pending Spaces enter pending deletion. Their retention deadline
is the later of the original account request deadline and an existing Space
deadline. Deleted Spaces are not reactivated. Default Spaces receive the same
scheduling behavior through migrations 161–162: the database permits only a
service cleanup for a pending account with completed prerequisite steps and a
currently leased native local step. Removing default status or transferring its
owner remains prohibited. The follow-up migration gives the trigger's existing
RLS helpers their required trusted schema resolution without changing an applied
migration checksum.

The account's avatar pointer and version are cleared. Migration 157 atomically
queues the former immutable object key or legacy fixed key; object storage
deletion remains the durable object worker's responsibility. Existing unfinished
upload intents retain their original delay so an in-flight upload cannot race
early deletion.

Installed Apps become recoverable and receive private-data deletion jobs for the
original account retention deadline. Their sessions are removed. Existing
recoverable, purging or purged installations and job attempts retain their state
and deadline; cleanup does not restart an already running purge. The account
write lock serializes this work with installation and App purge writers.

All effects, outboxes and the local completion receipt commit together. The final
lease check uses database wall time: expiration rolls the entire transaction back.
The receipt is `local_cleanup_scheduled`, and the parent becomes `scheduled`.
This records removal of local access and durable cleanup scheduling, **not proof
that remote objects or retained private data have already been erased**. The
worker uses a 25-second operation deadline, two-second lock timeout and
five-second statement timeout; failures retain a safe retry code.

Ten restricted-role PostgreSQL tests cover shared-content preservation, default
Space scheduling/protection, both prerequisite fences, stale/reclaimed leases,
actual expiration during SQL execution, whole-transaction rollback, real Space
lock contention and successful retry, immutable and legacy avatar intents,
unchanged App purge attempts, canceled execution, legacy-owner exclusion and
account lifecycle rechecks. The API test role cannot read billing tables.

Before production activation, the retention purge must reconcile owned Spaces
whose retention extends beyond the account deadline, await durable storage and
collaboration cleanup where required, erase all remaining account-private data,
and preserve shared attribution and financial identities needed for late events.
Large-account inventory bounds, provider/Space cascade reconciliation, pending
invitation handling, service-writer lifecycle guards and live realtime disconnect
must be included in the complete deletion release rehearsal. This stage alone
does not satisfy those gates.
