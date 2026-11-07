-- +goose Up
-- A chat lives on the computer that started it. The server keeps a thin row with
-- no transcript, so its runs can be admitted and titled, and names the computer
-- that keeps it. A chat no computer keeps yet (one a schedule started) waits in
-- that person's inbox until one takes it. Runs of a kept chat aren't retained past
-- their own expiry.
-- Chats from before this change are legacy: they never reach a computer that asks
-- for its own chats, and they expire through their normal retention.
SELECT set_config('app.rls_mode', 'service', true);
ALTER TABLE misty_ask_conversations ADD COLUMN kept_on_device_id text;
ALTER TABLE misty_ask_conversations ADD COLUMN legacy boolean NOT NULL DEFAULT false;
UPDATE misty_ask_conversations SET legacy = true;
CREATE INDEX misty_ask_conversations_inbox ON misty_ask_conversations(user_id)
    WHERE kept_on_device_id IS NULL AND NOT legacy AND deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS misty_ask_conversations_inbox;
ALTER TABLE misty_ask_conversations DROP COLUMN legacy, DROP COLUMN kept_on_device_id;
