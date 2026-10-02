-- +goose Up
-- Committed hints for the agent dispatcher. Each trigger fires only for changes
-- that can make dispatcher work due sooner. Deadlines that move later (runtime
-- heartbeats, observed-at stamps) need no hint: a worker that wakes early
-- recomputes its next deadline from the durable rows.

-- Invocation runtime queue (agent-runtime).
CREATE TRIGGER agent_runtime_delivery_insert_notify AFTER INSERT ON agent_runtime_deliveries
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('agent-runtime');
CREATE TRIGGER agent_runtime_delivery_update_notify AFTER UPDATE OF state,available_at,lease_expires_at ON agent_runtime_deliveries
FOR EACH ROW WHEN ((OLD.state,OLD.available_at,OLD.lease_expires_at) IS DISTINCT FROM (NEW.state,NEW.available_at,NEW.lease_expires_at))
EXECUTE FUNCTION misty_notify_worker_queue('agent-runtime');
-- A removed delivery row lets a pending approval resume be queued again.
CREATE TRIGGER agent_runtime_delivery_delete_notify AFTER DELETE ON agent_runtime_deliveries
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('agent-tasks');

CREATE TRIGGER ai_intervention_wait_insert_notify AFTER INSERT ON ai_intervention_waits
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('agent-runtime');
CREATE TRIGGER ai_intervention_wait_update_notify AFTER UPDATE OF state,expires_at ON ai_intervention_waits
FOR EACH ROW WHEN ((OLD.state,OLD.expires_at) IS DISTINCT FROM (NEW.state,NEW.expires_at))
EXECUTE FUNCTION misty_notify_worker_queue('agent-runtime');

CREATE TRIGGER agent_tool_approval_insert_notify AFTER INSERT ON agent_run_tool_approvals
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('agent-runtime','agent-tasks');
CREATE TRIGGER agent_tool_approval_update_notify AFTER UPDATE OF state,expires_at ON agent_run_tool_approvals
FOR EACH ROW WHEN ((OLD.state,OLD.expires_at) IS DISTINCT FROM (NEW.state,NEW.expires_at))
EXECUTE FUNCTION misty_notify_worker_queue('agent-runtime','agent-tasks');

CREATE TRIGGER ai_invocation_dispatch_insert_notify AFTER INSERT ON ai_invocations
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('agent-runtime');
CREATE TRIGGER ai_invocation_dispatch_update_notify
AFTER UPDATE OF state,runtime_run_id,agent_run_id,approval_wait_id,device_wait_hook_token,device_wait_expires_at ON ai_invocations
FOR EACH ROW WHEN ((OLD.state,OLD.runtime_run_id,OLD.agent_run_id,OLD.approval_wait_id,OLD.device_wait_hook_token,OLD.device_wait_expires_at)
 IS DISTINCT FROM (NEW.state,NEW.runtime_run_id,NEW.agent_run_id,NEW.approval_wait_id,NEW.device_wait_hook_token,NEW.device_wait_expires_at))
EXECUTE FUNCTION misty_notify_worker_queue('agent-runtime');

CREATE TRIGGER ai_invocation_context_insert_notify AFTER INSERT ON ai_invocation_contexts
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('agent-runtime');
CREATE TRIGGER ai_invocation_context_update_notify AFTER UPDATE OF state,capabilities,expires_at,opaque_ref ON ai_invocation_contexts
FOR EACH ROW WHEN ((OLD.state,OLD.capabilities,OLD.expires_at,OLD.opaque_ref) IS DISTINCT FROM (NEW.state,NEW.capabilities,NEW.expires_at,NEW.opaque_ref))
EXECUTE FUNCTION misty_notify_worker_queue('agent-runtime');

-- Space run queue (agent-tasks).
CREATE TRIGGER space_run_dispatch_insert_notify AFTER INSERT ON space_runs
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('agent-tasks');
CREATE TRIGGER space_run_dispatch_update_notify
AFTER UPDATE OF state,runtime_phase,approval_wait_id,device_wait_hook_token,device_wait_expires_at,runtime_run_id,execution_owner ON space_runs
FOR EACH ROW WHEN ((OLD.state,OLD.runtime_phase,OLD.approval_wait_id,OLD.device_wait_hook_token,OLD.device_wait_expires_at,OLD.runtime_run_id,OLD.execution_owner)
 IS DISTINCT FROM (NEW.state,NEW.runtime_phase,NEW.approval_wait_id,NEW.device_wait_hook_token,NEW.device_wait_expires_at,NEW.runtime_run_id,NEW.execution_owner))
EXECUTE FUNCTION misty_notify_worker_queue('agent-tasks');

CREATE TRIGGER agent_run_context_insert_notify AFTER INSERT ON agent_run_contexts
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('agent-tasks');
CREATE TRIGGER agent_run_context_update_notify AFTER UPDATE OF state,capabilities,expires_at,opaque_ref ON agent_run_contexts
FOR EACH ROW WHEN ((OLD.state,OLD.capabilities,OLD.expires_at,OLD.opaque_ref) IS DISTINCT FROM (NEW.state,NEW.capabilities,NEW.expires_at,NEW.opaque_ref))
EXECUTE FUNCTION misty_notify_worker_queue('agent-tasks');

CREATE TRIGGER agent_run_job_insert_notify AFTER INSERT OR DELETE ON agent_run_jobs
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('agent-tasks');
CREATE TRIGGER agent_run_job_update_notify AFTER UPDATE OF state,available_at,lease_expires_at ON agent_run_jobs
FOR EACH ROW WHEN ((OLD.state,OLD.available_at,OLD.lease_expires_at) IS DISTINCT FROM (NEW.state,NEW.available_at,NEW.lease_expires_at))
EXECUTE FUNCTION misty_notify_worker_queue('agent-tasks');

CREATE TRIGGER space_task_assignment_notify AFTER UPDATE OF assignee_agent_id,archived_at ON space_tasks
FOR EACH ROW WHEN ((OLD.assignee_agent_id,OLD.archived_at) IS DISTINCT FROM (NEW.assignee_agent_id,NEW.archived_at))
EXECUTE FUNCTION misty_notify_worker_queue('agent-tasks');

-- Device waits become ready when a device returns from the 90-second offline
-- window. Ordinary heartbeats from an online device publish nothing.
CREATE TRIGGER trusted_device_online_notify AFTER UPDATE OF last_seen_at,revoked_at ON trusted_devices
FOR EACH ROW WHEN (NEW.revoked_at IS NULL AND NEW.last_seen_at > now()-interval '90 seconds'
 AND (OLD.last_seen_at IS NULL OR OLD.last_seen_at <= now()-interval '90 seconds' OR OLD.revoked_at IS NOT NULL))
EXECUTE FUNCTION misty_notify_worker_queue('agent-runtime','agent-tasks');

-- +goose Down
DROP TRIGGER IF EXISTS trusted_device_online_notify ON trusted_devices;
DROP TRIGGER IF EXISTS space_task_assignment_notify ON space_tasks;
DROP TRIGGER IF EXISTS agent_run_job_update_notify ON agent_run_jobs;
DROP TRIGGER IF EXISTS agent_run_job_insert_notify ON agent_run_jobs;
DROP TRIGGER IF EXISTS agent_run_context_update_notify ON agent_run_contexts;
DROP TRIGGER IF EXISTS agent_run_context_insert_notify ON agent_run_contexts;
DROP TRIGGER IF EXISTS space_run_dispatch_update_notify ON space_runs;
DROP TRIGGER IF EXISTS space_run_dispatch_insert_notify ON space_runs;
DROP TRIGGER IF EXISTS ai_invocation_context_update_notify ON ai_invocation_contexts;
DROP TRIGGER IF EXISTS ai_invocation_context_insert_notify ON ai_invocation_contexts;
DROP TRIGGER IF EXISTS ai_invocation_dispatch_update_notify ON ai_invocations;
DROP TRIGGER IF EXISTS ai_invocation_dispatch_insert_notify ON ai_invocations;
DROP TRIGGER IF EXISTS agent_tool_approval_update_notify ON agent_run_tool_approvals;
DROP TRIGGER IF EXISTS agent_tool_approval_insert_notify ON agent_run_tool_approvals;
DROP TRIGGER IF EXISTS ai_intervention_wait_update_notify ON ai_intervention_waits;
DROP TRIGGER IF EXISTS ai_intervention_wait_insert_notify ON ai_intervention_waits;
DROP TRIGGER IF EXISTS agent_runtime_delivery_delete_notify ON agent_runtime_deliveries;
DROP TRIGGER IF EXISTS agent_runtime_delivery_update_notify ON agent_runtime_deliveries;
DROP TRIGGER IF EXISTS agent_runtime_delivery_insert_notify ON agent_runtime_deliveries;
