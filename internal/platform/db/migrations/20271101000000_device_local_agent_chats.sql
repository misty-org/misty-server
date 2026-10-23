-- +goose Up
-- Agent chats live on the device they were started on, like Claude Code and Codex
-- (docs/agents/chats-local.md). The server keeps a thin row per chat so
-- its runs can be admitted, billed and titled, and names the device that keeps it.
-- A chat no device keeps yet (one Misty started by email, Slack or a schedule) waits
-- in that person's inbox until a device takes it. Runs of a chat a device keeps are
-- no longer retained past their own expiry: the device holds the transcript.
-- Chats from before this change are marked legacy: each device copies them once, and
-- they keep their 30-day retention.
ALTER TABLE misty_ask_conversations ADD COLUMN kept_on_device_id text;
ALTER TABLE misty_ask_conversations ADD COLUMN legacy boolean NOT NULL DEFAULT false;
UPDATE misty_ask_conversations SET legacy = true;
CREATE INDEX misty_ask_conversations_inbox ON misty_ask_conversations(user_id)
    WHERE kept_on_device_id IS NULL AND NOT legacy AND deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS misty_ask_conversations_inbox;
ALTER TABLE misty_ask_conversations DROP COLUMN IF EXISTS legacy;
ALTER TABLE misty_ask_conversations DROP COLUMN IF EXISTS kept_on_device_id;
