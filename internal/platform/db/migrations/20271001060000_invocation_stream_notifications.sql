-- +goose Up
-- One coalesced hint per invocation/transaction, independent of viewer count.
-- +goose StatementBegin
CREATE FUNCTION misty_notify_invocation_stream() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE invocation text;
BEGIN
 IF TG_TABLE_NAME='ai_invocations' THEN
  IF TG_OP='DELETE' THEN invocation:=OLD.id; ELSE invocation:=NEW.id; END IF;
  IF TG_OP='UPDATE' AND (OLD.state,OLD.user_id,OLD.expires_at) IS NOT DISTINCT FROM
   (NEW.state,NEW.user_id,NEW.expires_at) THEN RETURN NULL; END IF;
 ELSE
  IF TG_OP='DELETE' THEN invocation:=OLD.invocation_id; ELSE invocation:=NEW.invocation_id; END IF;
 END IF;
 PERFORM pg_notify('misty_worker_events','invocation-event:' || encode(sha256(convert_to(invocation,'UTF8')),'hex'));
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER invocation_event_stream_notify AFTER INSERT OR DELETE ON ai_invocation_events
FOR EACH ROW EXECUTE FUNCTION misty_notify_invocation_stream();
CREATE TRIGGER invocation_state_stream_notify AFTER INSERT OR DELETE OR UPDATE OF state,user_id,expires_at ON ai_invocations
FOR EACH ROW EXECUTE FUNCTION misty_notify_invocation_stream();

-- +goose Down
DROP TRIGGER IF EXISTS invocation_state_stream_notify ON ai_invocations;
DROP TRIGGER IF EXISTS invocation_event_stream_notify ON ai_invocation_events;
DROP FUNCTION IF EXISTS misty_notify_invocation_stream();
