-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- NOTIFY is delivered only after commit. Payloads contain ownership and routing
-- IDs only; clients always re-read authorized snapshots. Reconnects reset them.
CREATE FUNCTION misty_notify_account_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE item jsonb; owner_id text; record_id text;
BEGIN
  IF TG_OP = 'DELETE' THEN item := to_jsonb(OLD); ELSE item := to_jsonb(NEW); END IF;
  IF TG_OP = 'UPDATE' AND TG_ARGV[0] IN ('runs','invocations','jobs')
     AND to_jsonb(OLD)->>'state' IS NOT DISTINCT FROM item->>'state'
     AND to_jsonb(OLD)->>'progress' IS NOT DISTINCT FROM item->>'progress' THEN RETURN NULL; END IF;
  IF TG_ARGV[0] = 'jobs' AND (TG_OP = 'DELETE' OR item ->> 'state' <> 'queued') THEN RETURN NULL; END IF;
  owner_id := item ->> TG_ARGV[1];
  record_id := COALESCE(item ->> 'id', item ->> 'agent_id');
  IF owner_id IS NOT NULL AND owner_id <> '' THEN
    PERFORM pg_notify('misty_account_events', json_build_object('userId', owner_id, 'topic', TG_ARGV[0], 'id', record_id)::text);
  END IF;
  RETURN NULL;
END $$;
CREATE TRIGGER misty_agent_changes AFTER INSERT OR UPDATE OR DELETE ON misty_ask_identities
FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('agents', 'owner_user_id');
CREATE TRIGGER misty_assignment_changes AFTER INSERT OR UPDATE OR DELETE ON misty_agent_app_assignments
FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('agents', 'owner_user_id');
CREATE TRIGGER misty_run_changes AFTER INSERT OR UPDATE OF state, progress OR DELETE ON space_runs
FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('runs', 'requesting_member_id');
CREATE TRIGGER misty_invocation_changes AFTER INSERT OR UPDATE OF state OR DELETE ON ai_invocations
FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('invocations', 'user_id');
CREATE TRIGGER misty_job_changes AFTER INSERT OR UPDATE OF state OR DELETE ON workflow_device_node_jobs
FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('jobs', 'user_id');
CREATE TRIGGER misty_approval_changes AFTER INSERT OR UPDATE OR DELETE ON agent_run_tool_approvals
FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('approvals', 'owner_user_id');
CREATE TRIGGER misty_run_approval_changes AFTER INSERT OR UPDATE OR DELETE ON space_run_approvals
FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('approvals', 'requested_from_user_id');
CREATE TRIGGER misty_intervention_changes AFTER INSERT OR UPDATE OR DELETE ON ai_intervention_waits
FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('interventions', 'user_id');
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TRIGGER misty_agent_changes ON misty_ask_identities;
DROP TRIGGER misty_assignment_changes ON misty_agent_app_assignments;
DROP TRIGGER misty_run_changes ON space_runs;
DROP TRIGGER misty_invocation_changes ON ai_invocations;
DROP TRIGGER misty_job_changes ON workflow_device_node_jobs;
DROP TRIGGER misty_approval_changes ON agent_run_tool_approvals;
DROP TRIGGER misty_run_approval_changes ON space_run_approvals;
DROP TRIGGER misty_intervention_changes ON ai_intervention_waits;
DROP FUNCTION misty_notify_account_change();
-- +goose StatementEnd
