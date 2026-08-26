-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';

ALTER TABLE agent_conversations
    ADD COLUMN reasoning_effort TEXT NOT NULL DEFAULT ''
        CHECK (reasoning_effort IN ('','low','medium','high'));

CREATE TABLE ai_conversation_attachments (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    conversation_id TEXT REFERENCES agent_conversations(id) ON DELETE CASCADE,
    invocation_id TEXT REFERENCES ai_invocations(id) ON DELETE SET NULL,
    scope TEXT NOT NULL CHECK(scope IN ('conversation','visual_query')),
    display_name TEXT NOT NULL CHECK(char_length(display_name) BETWEEN 1 AND 255),
    mime_type TEXT NOT NULL CHECK(mime_type IN ('image/jpeg','image/png','image/webp')),
    byte_size BIGINT NOT NULL CHECK(byte_size BETWEEN 1 AND 10485760),
    sha256 TEXT NOT NULL CHECK(sha256 ~ '^[0-9a-f]{64}$'),
    width INTEGER NOT NULL CHECK(width BETWEEN 1 AND 16384),
    height INTEGER NOT NULL CHECK(height BETWEEN 1 AND 16384),
    object_key TEXT NOT NULL UNIQUE,
    model_mime_type TEXT NOT NULL CHECK(model_mime_type IN ('image/jpeg','image/png','image/webp')),
    model_byte_size BIGINT NOT NULL CHECK(model_byte_size BETWEEN 1 AND 1048576),
    model_sha256 TEXT NOT NULL CHECK(model_sha256 ~ '^[0-9a-f]{64}$'),
    model_width INTEGER NOT NULL CHECK(model_width BETWEEN 1 AND 2048),
    model_height INTEGER NOT NULL CHECK(model_height BETWEEN 1 AND 2048),
    model_object_key TEXT NOT NULL UNIQUE,
    lifecycle_state TEXT NOT NULL DEFAULT 'pending'
        CHECK(lifecycle_state IN ('pending','ready','deleted')),
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK((scope='conversation' AND conversation_id IS NOT NULL) OR
          (scope='visual_query' AND conversation_id IS NULL AND expires_at IS NOT NULL))
);

CREATE INDEX ai_conversation_attachments_conversation_idx
    ON ai_conversation_attachments(conversation_id,created_at)
    WHERE lifecycle_state='ready';
CREATE INDEX ai_conversation_attachments_expiry_idx
    ON ai_conversation_attachments(expires_at)
    WHERE expires_at IS NOT NULL AND lifecycle_state<>'deleted';

ALTER TABLE ai_conversation_attachments ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_conversation_attachments FORCE ROW LEVEL SECURITY;
CREATE POLICY ai_conversation_attachments_policy ON ai_conversation_attachments FOR ALL
    USING (misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON ai_conversation_attachments TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS ai_conversation_attachments;
ALTER TABLE agent_conversations DROP COLUMN IF EXISTS reasoning_effort;
-- +goose StatementEnd
