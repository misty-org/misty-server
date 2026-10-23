-- +goose Up
-- Changes are coalesced hints with no caller/IP data. Reconnect reloads the
-- authoritative block set; retention is driven by each row's real deadline.
CREATE TRIGGER abuse_block_notify AFTER INSERT OR DELETE OR UPDATE OF blocked_until,block_seconds ON abuse_blocks
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('abuse-blocks','abuse-retention');

-- +goose Down
DROP TRIGGER IF EXISTS abuse_block_notify ON abuse_blocks;
