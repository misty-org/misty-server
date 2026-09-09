-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout='5s';
SELECT set_config('app.rls_mode','service',true);
ALTER TABLE workflow_device_node_jobs ADD COLUMN control_version INTEGER NOT NULL DEFAULT 1 CHECK(control_version IN (1,2));
ALTER TABLE workflow_device_node_jobs ALTER COLUMN control_version SET DEFAULT 2;
ALTER TABLE workflow_device_node_jobs ADD COLUMN deadline_at TIMESTAMPTZ;
UPDATE workflow_device_node_jobs SET deadline_at=created_at+INTERVAL '5 minutes';
ALTER TABLE workflow_device_node_jobs ALTER COLUMN deadline_at SET NOT NULL;
ALTER TABLE workflow_device_node_jobs ADD COLUMN runtime_run_id TEXT NOT NULL DEFAULT '';
ALTER TABLE workflow_device_node_jobs ADD COLUMN required_capability TEXT NOT NULL DEFAULT '';
UPDATE workflow_device_node_jobs j SET runtime_run_id=COALESCE((SELECT runtime_run_id FROM space_runs WHERE id=j.run_id),(SELECT runtime_run_id FROM ai_invocations WHERE id=j.invocation_id),''),
 required_capability=CASE WHEN operation LIKE 'browser.%' THEN operation WHEN operation='read_content' THEN 'files.read' ELSE '' END;
ALTER TABLE workflow_device_node_jobs ADD COLUMN execution_started_at TIMESTAMPTZ;
ALTER TABLE workflow_device_node_jobs ADD COLUMN cancel_requested_at TIMESTAMPTZ;
ALTER TABLE workflow_device_node_jobs ADD COLUMN delivery_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE workflow_device_node_jobs DROP CONSTRAINT workflow_device_node_jobs_state_check;
ALTER TABLE workflow_device_node_jobs ADD CONSTRAINT workflow_device_node_jobs_state_check
 CHECK(state IN ('queued','leased','executing','completed','failed','canceled','uncertain'));
CREATE INDEX workflow_device_execution_deadline_idx ON workflow_device_node_jobs(deadline_at) WHERE state IN ('queued','leased','executing');

CREATE FUNCTION misty_cancel_run_device_work() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.state IN ('completed','completed_with_errors','failed','canceled') AND NEW.state IS DISTINCT FROM OLD.state THEN
   UPDATE workflow_device_node_jobs SET
    cancel_requested_at=COALESCE(cancel_requested_at,clock_timestamp()),
    state=CASE WHEN state='queued' OR state='leased' AND control_version=2 AND execution_started_at IS NULL THEN 'canceled' ELSE state END,
    completed_at=CASE WHEN state='queued' OR state='leased' AND control_version=2 AND execution_started_at IS NULL THEN clock_timestamp() ELSE completed_at END
   WHERE (run_id=NEW.id OR invocation_id=NEW.id) AND state IN ('queued','leased','executing');
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER cancel_run_device_work AFTER UPDATE OF state ON space_runs FOR EACH ROW EXECUTE FUNCTION misty_cancel_run_device_work();
CREATE TRIGGER cancel_run_device_work AFTER UPDATE OF state ON ai_invocations FOR EACH ROW EXECUTE FUNCTION misty_cancel_run_device_work();
-- +goose StatementEnd
-- +goose Down
-- Preserve deadlines, cancellation and uncertain execution evidence on rollback.
SELECT 1;
