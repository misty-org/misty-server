-- +goose Up
ALTER TABLE agent_conversations
    ADD COLUMN state JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(state) = 'object'),
    ADD COLUMN active_until TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '2 hours'),
    ADD COLUMN retention_expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '30 days');

CREATE INDEX agent_conversations_retention_idx
    ON agent_conversations(retention_expires_at);

-- +goose Down
DROP INDEX IF EXISTS agent_conversations_retention_idx;
ALTER TABLE agent_conversations
    DROP COLUMN IF EXISTS retention_expires_at,
    DROP COLUMN IF EXISTS active_until,
    DROP COLUMN IF EXISTS state;
