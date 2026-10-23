-- +goose Up
-- Account events for client surfaces that used to poll. Each is a committed,
-- content-free hint; clients re-read their authorized snapshot on it and on reset.
-- Clients follow their own task list from these account events instead of polling.
CREATE TRIGGER scheduled_task_account_notify AFTER INSERT OR DELETE ON scheduled_tasks
FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('scheduled-tasks','user_id');
CREATE TRIGGER scheduled_task_account_update_notify AFTER UPDATE ON scheduled_tasks
FOR EACH ROW WHEN (OLD.* IS DISTINCT FROM NEW.*)
EXECUTE FUNCTION misty_notify_account_change('scheduled-tasks','user_id');
-- A delivered billing transition changes the account's usage; clients refresh
-- their allowance from this hint instead of polling.
-- +goose StatementBegin
CREATE FUNCTION misty_notify_billing_usage() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF COALESCE(NEW.payload->>'account_id','')<>'' THEN
    PERFORM pg_notify('misty_account_events', json_build_object('userId', NEW.payload->>'account_id', 'topic', 'usage')::text);
  END IF;
  RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER billing_usage_account_notify AFTER UPDATE OF delivered_at ON billing_adapter_outbox
FOR EACH ROW WHEN (OLD.delivered_at IS NULL AND NEW.delivered_at IS NOT NULL)
EXECUTE FUNCTION misty_notify_billing_usage();
-- An edit's rendition finishing or failing reaches its author's open viewers.
CREATE TRIGGER library_rendition_account_notify AFTER UPDATE OF rendition_state ON library_item_versions
FOR EACH ROW WHEN (OLD.rendition_state IS DISTINCT FROM NEW.rendition_state)
EXECUTE FUNCTION misty_notify_account_change('library-renditions','created_by_user_id');
-- Both devices in a pairing follow its state instead of polling it.
CREATE TRIGGER device_pairing_account_notify AFTER UPDATE OF state ON device_pairing_sessions
FOR EACH ROW WHEN (OLD.state IS DISTINCT FROM NEW.state)
EXECUTE FUNCTION misty_notify_account_change('device-pairing','owner_user_id');
-- Connected Devices follows peers from these hints: pairs, peer identity and
-- addressing, and a peer returning from the 90-second offline window. Peers
-- going offline need no hint; clients age heartbeats locally.
CREATE TRIGGER device_pair_account_notify AFTER INSERT OR UPDATE ON device_pairs
FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('devices','owner_user_id');
CREATE TRIGGER device_presence_insert_account_notify AFTER INSERT ON device_presence
FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('devices','owner_user_id');
CREATE TRIGGER device_presence_account_notify AFTER UPDATE ON device_presence
FOR EACH ROW WHEN ((OLD.p2p_endpoint_id,OLD.addressing,OLD.protocol_version) IS DISTINCT FROM (NEW.p2p_endpoint_id,NEW.addressing,NEW.protocol_version)
 OR OLD.last_heartbeat_at IS NULL OR OLD.last_heartbeat_at<=now()-interval '90 seconds')
EXECUTE FUNCTION misty_notify_account_change('devices','owner_user_id');
CREATE TRIGGER trusted_device_identity_account_notify AFTER UPDATE OF name,platform,p2p_endpoint_id,revoked_at ON trusted_devices
FOR EACH ROW WHEN ((OLD.name,OLD.platform,OLD.p2p_endpoint_id,OLD.revoked_at) IS DISTINCT FROM (NEW.name,NEW.platform,NEW.p2p_endpoint_id,NEW.revoked_at))
EXECUTE FUNCTION misty_notify_account_change('devices','user_id');

-- +goose Down
DROP TRIGGER IF EXISTS trusted_device_identity_account_notify ON trusted_devices;
DROP TRIGGER IF EXISTS device_presence_account_notify ON device_presence;
DROP TRIGGER IF EXISTS device_presence_insert_account_notify ON device_presence;
DROP TRIGGER IF EXISTS device_pair_account_notify ON device_pairs;
DROP TRIGGER IF EXISTS device_pairing_account_notify ON device_pairing_sessions;
DROP TRIGGER IF EXISTS library_rendition_account_notify ON library_item_versions;
DROP TRIGGER IF EXISTS billing_usage_account_notify ON billing_adapter_outbox;
DROP FUNCTION IF EXISTS misty_notify_billing_usage();
DROP TRIGGER IF EXISTS scheduled_task_account_update_notify ON scheduled_tasks;
DROP TRIGGER IF EXISTS scheduled_task_account_notify ON scheduled_tasks;
