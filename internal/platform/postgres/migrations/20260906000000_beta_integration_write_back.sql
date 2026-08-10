-- +goose Up
-- +goose StatementBegin
-- Adding a column takes ACCESS EXCLUSIVE, and a *pending* lock request queues
-- every new reader behind it. Waiting on a busy space_messages would therefore
-- freeze live Space chat rather than merely being slow. Fail fast instead and
-- let the operator retry during a quiet moment or after stopping the server.
SET LOCAL lock_timeout = '5s';

-- Beta write-back integrations: Discord conversation mirroring and Google
-- Calendar-backed tasks. Both add an explicit provenance record so an outward
-- write is always attributable, and so a provider update can never silently
-- overwrite something a person typed in Misty.

-- Provenance for a mirrored message. NULL means ordinary Misty-native chat,
-- which keeps every existing row and query valid.
ALTER TABLE space_messages
    ADD COLUMN origin JSONB CHECK (origin IS NULL OR jsonb_typeof(origin) = 'object');

-- One Space conversation mirrored to one Discord channel. Beta allows a single
-- link per channel per Space; the row is keyed by conversation so lifting that
-- cap later is additive rather than a schema change.
CREATE TABLE space_discord_links (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    integration_id TEXT NOT NULL REFERENCES space_integrations(id) ON DELETE CASCADE,
    conversation_id TEXT REFERENCES space_conversations(id) ON DELETE CASCADE,
    connected_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    guild_id TEXT NOT NULL CHECK (char_length(guild_id) BETWEEN 1 AND 64),
    guild_name TEXT NOT NULL DEFAULT '' CHECK (char_length(guild_name) <= 240),
    channel_id TEXT NOT NULL CHECK (char_length(channel_id) BETWEEN 1 AND 64),
    channel_name TEXT NOT NULL DEFAULT '' CHECK (char_length(channel_name) <= 240),
    direction TEXT NOT NULL DEFAULT 'two_way' CHECK (direction IN ('two_way','inbound','outbound')),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','syncing','active','needs_attention','disabled')),
    -- Highest Discord snowflake already imported. Discord's `after` parameter
    -- takes exactly this value, so the cursor doubles as the resume point.
    last_message_id TEXT NOT NULL DEFAULT '',
    last_synced_at TIMESTAMPTZ,
    last_error_code TEXT NOT NULL DEFAULT '',
    bot_user_id TEXT NOT NULL DEFAULT '',
    -- Optional webhook used to post under each Misty author's own name. The
    -- token is sealed with the same AEAD as every other provider secret.
    webhook_id TEXT NOT NULL DEFAULT '',
    webhook_token_ciphertext BYTEA,
    webhook_token_nonce BYTEA,
    disabled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(space_id,channel_id)
);
CREATE INDEX space_discord_links_channel_idx ON space_discord_links(guild_id,channel_id) WHERE disabled_at IS NULL;
CREATE INDEX space_discord_links_space_idx ON space_discord_links(space_id) WHERE disabled_at IS NULL;

-- Google Calendar-backed task state.
--   schedule          the live schedule Misty holds, possibly edited locally
--   calendar          the binding, including the snapshot last agreed with Google
--   conflicted_fields fields where a local edit and a Google update disagree
-- All three are NULL/empty for ordinary Misty-only tasks.
ALTER TABLE space_tasks
    ADD COLUMN schedule JSONB CHECK (schedule IS NULL OR jsonb_typeof(schedule) = 'object'),
    ADD COLUMN calendar JSONB CHECK (calendar IS NULL OR jsonb_typeof(calendar) = 'object'),
    ADD COLUMN conflicted_fields TEXT[] NOT NULL DEFAULT '{}';

-- Resolves a Google event back to its task during an incremental sync.
CREATE UNIQUE INDEX space_tasks_google_event_idx
    ON space_tasks((calendar->>'source_id'),(calendar->>'google_event_id'))
    WHERE calendar->>'google_event_id' IS NOT NULL AND archived_at IS NULL;

ALTER TABLE space_discord_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_discord_links FORCE ROW LEVEL SECURITY;
CREATE POLICY space_discord_links_member_policy ON space_discord_links FOR ALL
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_discord_links TO misty_app;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS space_tasks_google_event_idx;
ALTER TABLE space_tasks
    DROP COLUMN IF EXISTS conflicted_fields,
    DROP COLUMN IF EXISTS calendar,
    DROP COLUMN IF EXISTS schedule;
DROP TABLE IF EXISTS space_discord_links CASCADE;
ALTER TABLE space_messages DROP COLUMN IF EXISTS origin;
-- +goose StatementEnd
