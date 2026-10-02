package db

// Keep sending/unknown outcomes out of this predicate. A scheduler wakeup is
// never authority to replay an external action whose result is uncertain.
const socialWorkerEligibility = `EXISTS(SELECT 1 FROM social_bindings b
 WHERE b.id=c.binding_id AND b.status='active' AND b.disabled_at IS NULL)`

const socialWorkerDeadline = `SELECT min(due) FROM (
 (SELECT s.scheduled_at AS due FROM social_scheduled_messages s
 JOIN social_send_authorities a ON a.id=s.authority_id
 WHERE s.status='scheduled' AND a.allow_scheduled AND a.revoked_at IS NULL ORDER BY s.scheduled_at LIMIT 1)
 UNION ALL
 (SELECT GREATEST(c.available_at,c.lease_expires_at) AS due
 FROM social_outbound_commands c WHERE c.state='queued' AND ` + socialWorkerEligibility + `
 ORDER BY GREATEST(c.available_at,c.lease_expires_at) LIMIT 1)
 ) pending`

const billingWorkerDeadline = `SELECT min(due) FROM (
 SELECT min(expires_at) AS due FROM billing_adapter_intents WHERE state IN ('pending','abandoned')
 UNION ALL
 SELECT min(available_at) AS due FROM billing_adapter_outbox WHERE delivered_at IS NULL
 UNION ALL
 SELECT min(updated_at+interval '2 minutes') AS due FROM voice_usage_journal WHERE state='active'
 UNION ALL
 SELECT min(j.updated_at+interval '30 seconds') AS due FROM voice_usage_journal j
 JOIN billing_adapter_reservations r ON r.account_id=j.account_id AND r.reservation_id=j.reservation_id
 WHERE j.state='settlement_pending'
 ) pending`
