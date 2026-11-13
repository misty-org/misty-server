-- +goose Up
-- A note's commands are delivered one at a time in the order they were queued, and a command that
-- keeps failing is given up on (abandoned_at) instead of retried forever and holding up the
-- commands queued after it.
ALTER TABLE space_note_control_outbox ADD COLUMN abandoned_at timestamp with time zone;
CREATE INDEX space_note_control_outbox_note_order_idx ON space_note_control_outbox (note_id, created_at, id)
    WHERE delivered_at IS NULL AND abandoned_at IS NULL;
DROP INDEX IF EXISTS space_note_control_outbox_pending_idx;
CREATE INDEX space_note_control_outbox_pending_idx ON space_note_control_outbox (next_attempt_at)
    WHERE delivered_at IS NULL AND abandoned_at IS NULL;
DROP TRIGGER IF EXISTS note_control_worker_notify ON space_note_control_outbox;
CREATE TRIGGER note_control_worker_notify AFTER INSERT OR DELETE OR UPDATE OF next_attempt_at,delivered_at,abandoned_at ON space_note_control_outbox
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('note-control');

-- +goose Down
DROP TRIGGER IF EXISTS note_control_worker_notify ON space_note_control_outbox;
CREATE TRIGGER note_control_worker_notify AFTER INSERT OR DELETE OR UPDATE OF next_attempt_at,delivered_at ON space_note_control_outbox
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('note-control');
DROP INDEX IF EXISTS space_note_control_outbox_pending_idx;
CREATE INDEX space_note_control_outbox_pending_idx ON space_note_control_outbox (next_attempt_at)
    WHERE delivered_at IS NULL;
DROP INDEX IF EXISTS space_note_control_outbox_note_order_idx;
ALTER TABLE space_note_control_outbox DROP COLUMN abandoned_at;
