package db

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
