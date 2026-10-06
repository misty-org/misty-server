package db

// Due-time planning for scheduled runs and lifecycle deadlines. Each branch
// matches its claim predicate, including the account-level AI switch, so a row
// the claim would skip never keeps its queue due.

const scheduleOwnerEnabled = `COALESCE((SELECT s.enabled FROM ai_user_settings s WHERE s.user_id=t.user_id),TRUE)`

// Idle schedules use the next_run_at index. A running claim is due again only
// when its lease lapses; a running row without a lease cannot be claimed.
func scheduleDeadline(table, claimable string) string {
	return `(SELECT t.next_run_at AS due FROM ` + table + ` t WHERE t.enabled AND t.state<>'running' AND ` + scheduleOwnerEnabled + claimable + `
  ORDER BY t.next_run_at LIMIT 1)
 UNION ALL
 (SELECT GREATEST(t.next_run_at,t.lease_until) AS due FROM ` + table + ` t WHERE t.enabled AND t.state='running' AND t.lease_until IS NOT NULL
  AND ` + scheduleOwnerEnabled + claimable + ` ORDER BY 1 LIMIT 1)`
}

// A turned-off workflow keeps its schedule, but the claim skips it.
const workflowEnabled = ` AND EXISTS(SELECT 1 FROM agent_methods m WHERE m.id=t.method_id AND m.user_id=t.user_id AND m.enabled)`

var scheduledWorkerDeadline = `SELECT min(due) FROM (` + scheduleDeadline("ai_recaps", "") + ` UNION ALL ` + scheduleDeadline("workflow_schedules", workflowEnabled) + `) pending`

const aiCleanupWorkerDeadline = `SELECT available_at FROM ai_cleanup_jobs
 WHERE state IN ('queued','failed') AND attempts<20 ORDER BY available_at,created_at LIMIT 1`

// Processing requests are retried by the queue's error backoff until their
// provider and storage cleanup succeeds.
const accountDeletionWorkerDeadline = `SELECT min(due) FROM (
 (SELECT clock_timestamp() AS due FROM account_deletion_requests WHERE cleanup_owner='go' AND status='processing' LIMIT 1)
 UNION ALL
 (SELECT purge_after AS due FROM account_deletion_requests WHERE cleanup_owner='go' AND status='scheduled' ORDER BY purge_after LIMIT 1)
 ) pending`

const renditionReservationWorkerDeadline = `SELECT expires_at FROM space_rendition_reservations WHERE state='active' ORDER BY expires_at LIMIT 1`
