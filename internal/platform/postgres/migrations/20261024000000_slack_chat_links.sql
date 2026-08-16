-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';

-- Slack channels are first-class provider conversations, parallel to Discord,
-- while continuing to reuse provider_shared_resources for consent, discovery,
-- event routing, content indexing, and credential access.
ALTER TABLE space_conversations DROP CONSTRAINT IF EXISTS space_conversations_origin_check;
ALTER TABLE space_conversations ADD CONSTRAINT space_conversations_origin_check
    CHECK (origin IN ('misty','discord','slack'));
CREATE UNIQUE INDEX space_conversations_slack_resource_idx
    ON space_conversations(space_id,external_resource_id)
    WHERE origin='slack' AND external_resource_id<>'';
CREATE UNIQUE INDEX space_messages_slack_external_idx
    ON space_messages(space_id,(origin->>'external_id'))
    WHERE origin->>'system'='slack' AND origin->>'external_id'<>'';

CREATE TABLE space_slack_links (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    integration_id TEXT NOT NULL REFERENCES space_integrations(id) ON DELETE CASCADE,
    shared_resource_id TEXT NOT NULL REFERENCES provider_shared_resources(id) ON DELETE CASCADE,
    conversation_id TEXT NOT NULL REFERENCES space_conversations(id) ON DELETE CASCADE,
    connected_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    team_id TEXT NOT NULL CHECK (char_length(team_id) BETWEEN 1 AND 120),
    team_name TEXT NOT NULL DEFAULT '' CHECK (char_length(team_name) <= 240),
    channel_id TEXT NOT NULL CHECK (char_length(channel_id) BETWEEN 1 AND 120),
    channel_name TEXT NOT NULL DEFAULT '' CHECK (char_length(channel_name) <= 240),
    direction TEXT NOT NULL DEFAULT 'two_way'
        CHECK (direction IN ('two_way','inbound','outbound')),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','syncing','active','needs_attention','disabled')),
    last_message_ts TEXT NOT NULL DEFAULT '' CHECK (char_length(last_message_ts) <= 64),
    last_synced_at TIMESTAMPTZ,
    last_error_code TEXT NOT NULL DEFAULT '' CHECK (char_length(last_error_code) <= 120),
    bot_user_id TEXT NOT NULL DEFAULT '' CHECK (char_length(bot_user_id) <= 120),
    disabled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(space_id,channel_id),
    UNIQUE(shared_resource_id)
);
CREATE INDEX space_slack_links_channel_idx
    ON space_slack_links(team_id,channel_id) WHERE disabled_at IS NULL;
CREATE INDEX space_slack_links_space_idx
    ON space_slack_links(space_id) WHERE disabled_at IS NULL;

ALTER TABLE space_slack_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_slack_links FORCE ROW LEVEL SECURITY;
CREATE POLICY space_slack_links_member ON space_slack_links FOR ALL
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_slack_links TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: Slack-origin conversations and provenance may already exist.
SELECT 1;
