package db

// Due-time planning for the agent dispatcher. Each branch mirrors one scan in
// the matching Process function, so work the scan clears leaves the due set.
// A branch that can stay due after its scan ran would make the queue spin.

const aiRuntimeStaleAfter = `interval '5 minutes'`
const personalRunStaleAfter = `interval '12 minutes'`

const agentRuntimeWorkerDeadline = `SELECT min(due) FROM (
 (SELECT w.expires_at AS due FROM ai_intervention_waits w JOIN ` + interventionRunsSQL + ` r ON r.id=w.run_id AND r.user_id=w.user_id
  WHERE w.state='pending' AND r.state='awaiting_intervention' ORDER BY w.expires_at LIMIT 1)
 UNION ALL
 (SELECT a.expires_at AS due FROM agent_run_tool_approvals a JOIN ai_invocations i ON i.id=a.invocation_id
  WHERE a.state='pending' AND i.state='awaiting_approval' AND i.approval_wait_id=a.id ORDER BY a.expires_at LIMIT 1)
 UNION ALL
 (SELECT CASE WHEN ` + aiDeviceReady + ` THEN clock_timestamp() ELSE i.device_wait_expires_at END AS due FROM ai_invocations i
  WHERE i.state='awaiting_device' AND i.device_wait_hook_token<>'' AND COALESCE(i.agent_run_id,'')='' ORDER BY 1 LIMIT 1)
 UNION ALL
 (SELECT GREATEST(COALESCE(i.runtime_observed_at,i.created_at),COALESCE(i.runtime_heartbeat_at,i.created_at))+` + aiRuntimeStaleAfter + ` AS due
  FROM ai_invocations i WHERE COALESCE(i.agent_run_id,'')='' AND i.runtime_run_id<>''
  AND i.state IN ('running','awaiting_approval','awaiting_device','awaiting_intervention','awaiting_timer')
  AND NOT EXISTS(SELECT 1 FROM agent_runtime_deliveries d WHERE d.run_id=i.id AND d.operation='runtime.reconcile' AND d.state IN ('pending','leased'))
  ORDER BY 1 LIMIT 1)
 UNION ALL
 (SELECT CASE WHEN state='pending' THEN available_at ELSE lease_expires_at END AS due FROM agent_runtime_deliveries
  WHERE state IN ('pending','leased') ORDER BY 1 LIMIT 1)
 ) pending`

const personalRunDeviceReady = `EXISTS(SELECT 1 FROM agent_run_contexts c JOIN trusted_devices d ON d.id=c.device_id
 WHERE c.run_id=r.id AND c.state='attached' AND c.expires_at>NOW() AND d.user_id=r.owner_user_id
 AND r.device_wait_scope_id<>'' AND c.opaque_ref=r.device_wait_scope_id
 AND r.device_wait_capability<>'' AND c.capabilities ? r.device_wait_capability
 AND d.revoked_at IS NULL AND d.last_seen_at>NOW()-INTERVAL '90 seconds')`

// Approval resumes are due only until their delivery row exists. Queuing is
// idempotent by delivery ID, so a run whose delivery exists in any state cannot
// be advanced by another scan; it waits for the delivery outcome instead.
const approvalResumeUnqueued = `NOT EXISTS(SELECT 1 FROM agent_runtime_deliveries d WHERE d.id='approval.resume:'||r.id||':'||a.id)`

const personalAgentTaskJobClaimable = `r.execution_owner='go' AND r.state IN ('queued','running')
 AND (j.task_id IS NULL OR (t.assignee_agent_id=j.agent_id AND t.archived_at IS NULL))
 AND NOT EXISTS(SELECT 1 FROM agent_run_jobs active WHERE active.agent_id=j.agent_id AND active.run_id<>j.run_id AND active.state='dispatched')`

const agentTaskWorkerDeadline = `SELECT min(due) FROM (
 (SELECT clock_timestamp() AS due FROM agent_run_tool_approvals a JOIN space_runs r ON r.id=a.run_id
  WHERE a.state IN ('approved','denied') AND r.approval_wait_id=a.id AND r.state='running'
  AND r.runtime_phase='approval_resume_pending' AND ` + approvalResumeUnqueued + ` LIMIT 1)
 UNION ALL
 (SELECT expires_at AS due FROM agent_run_tool_approvals WHERE state='pending' ORDER BY expires_at LIMIT 1)
 UNION ALL
 (SELECT clock_timestamp() AS due FROM agent_run_tool_approvals a JOIN space_runs r ON r.id=a.run_id
  WHERE a.state='expired' AND r.approval_wait_id=a.id AND r.state='awaiting_approval' LIMIT 1)
 UNION ALL
 (SELECT CASE WHEN ` + personalRunDeviceReady + ` THEN clock_timestamp() ELSE r.device_wait_expires_at END AS due FROM space_runs r
  WHERE r.state='awaiting_device' AND r.device_wait_hook_token<>'' ORDER BY 1 LIMIT 1)
 UNION ALL
 (SELECT COALESCE(r.runtime_heartbeat_at,r.updated_at)+` + personalRunStaleAfter + ` AS due FROM agent_run_jobs j JOIN space_runs r ON r.id=j.run_id
  WHERE j.state='dispatched' AND r.execution_owner='go' AND r.runtime_run_id<>''
  AND r.state IN ('running','awaiting_approval','awaiting_device','awaiting_intervention') ORDER BY 1 LIMIT 1)
 UNION ALL
 (SELECT clock_timestamp() AS due FROM agent_run_jobs j JOIN space_runs r ON r.id=j.run_id
  WHERE r.execution_owner='go' AND j.state IN ('queued','leased','dispatched')
  AND r.state IN ('completed','completed_with_errors','failed','canceled','rejected') LIMIT 1)
 UNION ALL
 (SELECT CASE WHEN j.state='queued' THEN j.available_at ELSE j.lease_expires_at END AS due
  FROM agent_run_jobs j JOIN space_runs r ON r.id=j.run_id LEFT JOIN space_tasks t ON t.id=j.task_id
  WHERE j.state IN ('queued','leased') AND ` + personalAgentTaskJobClaimable + ` ORDER BY 1 LIMIT 1)
 ) pending`
