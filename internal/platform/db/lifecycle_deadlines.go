package db

const accountDeletionWorkerDeadline = `SELECT min(due) FROM (
 (SELECT clock_timestamp() AS due FROM account_deletion_requests WHERE cleanup_owner='go' AND status='processing' LIMIT 1)
 UNION ALL
 (SELECT purge_after AS due FROM account_deletion_requests WHERE cleanup_owner='go' AND status='scheduled' ORDER BY purge_after LIMIT 1)
 UNION ALL
 (SELECT c.delete_at AS due
  FROM account_controls c JOIN users u ON u.id=c.user_id AND u.lifecycle_state='active'
  WHERE c.delete_at IS NOT NULL ORDER BY 1 LIMIT 1)
 ) pending`
