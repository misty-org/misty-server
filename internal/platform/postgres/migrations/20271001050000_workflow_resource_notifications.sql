-- +goose Up
-- Per-resource committed hints share the worker listener. Raw resource names,
-- fingerprints and run identities are never notification payloads.
-- +goose StatementBegin
CREATE FUNCTION misty_notify_resource_lease() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE resource text;
BEGIN
 IF TG_OP='DELETE' THEN resource:=OLD.resource_key; ELSE resource:=NEW.resource_key; END IF;
 IF TG_OP='UPDATE' AND (OLD.resource_key,OLD.expires_at,OLD.run_id,OLD.node_id) IS NOT DISTINCT FROM
  (NEW.resource_key,NEW.expires_at,NEW.run_id,NEW.node_id) THEN RETURN NULL; END IF;
 PERFORM pg_notify('misty_worker_events','resource-lease:' || encode(sha256(convert_to(resource,'UTF8')),'hex'));
 IF TG_OP='UPDATE' AND OLD.resource_key IS DISTINCT FROM NEW.resource_key THEN
  PERFORM pg_notify('misty_worker_events','resource-lease:' || encode(sha256(convert_to(OLD.resource_key,'UTF8')),'hex'));
 END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER workflow_resource_lease_notify AFTER INSERT OR UPDATE OR DELETE ON space_workflow_resource_leases
FOR EACH ROW EXECUTE FUNCTION misty_notify_resource_lease();

-- +goose Down
DROP TRIGGER IF EXISTS workflow_resource_lease_notify ON space_workflow_resource_leases;
DROP FUNCTION IF EXISTS misty_notify_resource_lease();
