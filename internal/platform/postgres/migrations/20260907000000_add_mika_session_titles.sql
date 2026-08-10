-- +goose Up
-- +goose StatementBegin
-- Mika sessions were resumable by id but never listable: a client that lost its
-- local state had no way to ask which sessions an account owns, so the desktop
-- app kept its own in-memory list that died with the process. Titles live here
-- so a session opened on one device is recognisable on another.
ALTER TABLE agent_conversations ADD COLUMN IF NOT EXISTS title TEXT NOT NULL DEFAULT '';

-- Supports the listing query, which is always scoped to one account and ordered
-- by recency.
CREATE INDEX IF NOT EXISTS agent_conversations_user_recent_idx
    ON agent_conversations(user_id, updated_at DESC)
    WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS agent_conversations_user_recent_idx;
ALTER TABLE agent_conversations DROP COLUMN IF EXISTS title;
-- +goose StatementEnd
