-- +goose Up
-- Completion waiters use a separate topic from queued-job discovery. Lease
-- heartbeat writes must wake neither one; notifications commit with the state.
-- +goose StatementBegin
CREATE FUNCTION misty_notify_device_job_state() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.state IS DISTINCT FROM NEW.state THEN
    PERFORM pg_notify('misty_account_events', json_build_object(
      'userId', NEW.user_id, 'topic', 'job-state', 'id', NEW.id)::text);
  END IF;
  RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER workflow_device_job_state_notify
AFTER UPDATE OF state ON workflow_device_node_jobs
FOR EACH ROW EXECUTE FUNCTION misty_notify_device_job_state();

-- +goose Down
DROP TRIGGER IF EXISTS workflow_device_job_state_notify ON workflow_device_node_jobs;
DROP FUNCTION IF EXISTS misty_notify_device_job_state();
