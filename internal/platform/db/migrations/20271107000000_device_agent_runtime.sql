-- +goose Up
-- Agent runs work on the person's desktop. A queued
-- runtime delivery tells the person's desktops on the 'runtime' account topic, so the
-- desktop that keeps the run claims it at once instead of on its next check.
-- +goose StatementBegin
CREATE FUNCTION misty_notify_runtime_delivery() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.state = 'pending' AND NEW.user_id IS NOT NULL THEN
    PERFORM pg_notify('misty_account_events', json_build_object(
      'userId', NEW.user_id, 'topic', 'runtime', 'id', NEW.id)::text);
  END IF;
  RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER agent_runtime_delivery_device_notify
AFTER INSERT OR UPDATE OF state,available_at ON agent_runtime_deliveries
FOR EACH ROW EXECUTE FUNCTION misty_notify_runtime_delivery();

-- +goose Down
DROP TRIGGER IF EXISTS agent_runtime_delivery_device_notify ON agent_runtime_deliveries;
DROP FUNCTION IF EXISTS misty_notify_runtime_delivery();
