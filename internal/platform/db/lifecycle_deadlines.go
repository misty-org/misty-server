package db

// Due-time planning for lifecycle deadlines.

// Processing requests are retried by the queue's error backoff until their
// provider and storage cleanup succeeds. A closed account is due on its
// deletion date (matching DueClosedAccounts).
const accountDeletionWorkerDeadline = `SELECT min(due) FROM (
 (SELECT clock_timestamp() AS due FROM account_deletion_requests WHERE cleanup_owner='go' AND status='processing' LIMIT 1)
 UNION ALL
 (SELECT purge_after AS due FROM account_deletion_requests WHERE cleanup_owner='go' AND status='scheduled' ORDER BY purge_after LIMIT 1)
 UNION ALL
 (SELECT c.delete_at AS due
  FROM account_controls c JOIN users u ON u.id=c.user_id AND u.lifecycle_state='active'
  WHERE c.delete_at IS NOT NULL ORDER BY 1 LIMIT 1)
 ) pending`

const renditionReservationWorkerDeadline = `SELECT expires_at FROM space_rendition_reservations WHERE state='active' ORDER BY expires_at LIMIT 1`
