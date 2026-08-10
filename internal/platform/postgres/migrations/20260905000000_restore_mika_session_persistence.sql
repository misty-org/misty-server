-- +goose Up
-- +goose StatementBegin
-- The unified Agent migration retired the legacy device/folder Agent tables,
-- but Mika's account-level resumable session store still uses these two
-- conversation tables. Restore only that persistence boundary; the retired
-- Agent definition/job runtime remains removed.
CREATE TABLE agent_conversations (
    id TEXT PRIMARY KEY CHECK (id ~ '^conversation_[0-9a-f-]{36}$'),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    state JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(state) = 'object'),
    active_until TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '2 hours'),
    retention_expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '30 days'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE TABLE agent_conversation_events (
    id BIGSERIAL PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES agent_conversations(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN ('user_message','assistant_message','tool_call','tool_result','error')),
    data JSONB NOT NULL CHECK (jsonb_typeof(data) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX agent_conversations_retention_idx ON agent_conversations(retention_expires_at);
CREATE INDEX agent_conversation_events_conversation_idx ON agent_conversation_events(conversation_id, id);

ALTER TABLE agent_conversations ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_conversations FORCE ROW LEVEL SECURITY;
ALTER TABLE agent_conversation_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_conversation_events FORCE ROW LEVEL SECURITY;

CREATE POLICY agent_conversations_policy ON agent_conversations FOR ALL
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());
CREATE POLICY agent_conversation_events_policy ON agent_conversation_events FOR ALL
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON agent_conversations, agent_conversation_events TO misty_app;
        GRANT USAGE, SELECT ON SEQUENCE agent_conversation_events_id_seq TO misty_app;
        -- Repair provider grants for installations where the application role
        -- was created after the provider-runtime migration ran.
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_integrations, space_provider_credentials, provider_oauth_states TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_conversation_events, agent_conversations CASCADE;
-- +goose StatementEnd
