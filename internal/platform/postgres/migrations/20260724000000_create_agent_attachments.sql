-- +goose Up
-- +goose StatementBegin
CREATE TABLE agent_attachments (
    id TEXT PRIMARY KEY CHECK (id ~ '^attachment_[0-9a-f-]{36}$'),
    job_id TEXT NOT NULL REFERENCES agent_jobs(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requester_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 255),
    media_type TEXT NOT NULL CHECK (char_length(media_type) BETWEEN 1 AND 255),
    plaintext_byte_size BIGINT NOT NULL CHECK (plaintext_byte_size BETWEEN 1 AND 52428800),
    ciphertext_byte_size BIGINT NOT NULL CHECK (ciphertext_byte_size BETWEEN 1 AND 52494336),
    page_count INTEGER NOT NULL DEFAULT 0 CHECK (page_count BETWEEN 0 AND 200),
    storage_key TEXT NOT NULL UNIQUE CHECK (storage_key ~ '^agents/[0-9a-f-]{36}/[0-9a-f-]{36}$'),
    ciphertext_sha256 TEXT NOT NULL CHECK (ciphertext_sha256 ~ '^[0-9a-f]{64}$'),
    wrapped_data_key TEXT CHECK (wrapped_data_key IS NULL OR char_length(wrapped_data_key) BETWEEN 16 AND 8192),
    key_wrap_algorithm TEXT NOT NULL CHECK (key_wrap_algorithm IN ('RSA-OAEP-SHA256','AES-KW','KMS')),
    key_wrap_key_id TEXT NOT NULL CHECK (char_length(key_wrap_key_id) BETWEEN 1 AND 512),
    content_encryption TEXT NOT NULL DEFAULT 'AES-256-GCM' CHECK (content_encryption = 'AES-256-GCM'),
    upload_token_hash TEXT NOT NULL CHECK (upload_token_hash ~ '^[0-9a-f]{64}$'),
    state TEXT NOT NULL DEFAULT 'initiated' CHECK (state IN ('initiated','ready','deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    upload_expires_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at <= created_at + INTERVAL '24 hours'),
    finalized_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ
);

CREATE TABLE agent_artifacts (
    id TEXT PRIMARY KEY CHECK (id ~ '^artifact_[0-9a-f-]{36}$'),
    job_id TEXT NOT NULL REFERENCES agent_jobs(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope_id TEXT NOT NULL CHECK (scope_id ~ '^scope_[A-Za-z0-9_-]{8,128}$'),
    kind TEXT NOT NULL CHECK (kind IN ('file','message','document_answer')),
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 255),
    relative_location TEXT CHECK (relative_location IS NULL OR (char_length(relative_location) BETWEEN 1 AND 1024 AND relative_location !~ '^(/|[A-Za-z]:[\\/])')),
    citations JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(citations) = 'array'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (job_id, kind, display_name)
);

CREATE INDEX agent_attachments_job_idx ON agent_attachments(job_id, created_at);
CREATE INDEX agent_attachments_expiry_idx ON agent_attachments(expires_at) WHERE state <> 'deleted';
CREATE INDEX agent_artifacts_job_idx ON agent_artifacts(job_id, created_at);

ALTER TABLE agent_attachments ENABLE ROW LEVEL SECURITY; ALTER TABLE agent_attachments FORCE ROW LEVEL SECURITY;
ALTER TABLE agent_artifacts ENABLE ROW LEVEL SECURITY; ALTER TABLE agent_artifacts FORCE ROW LEVEL SECURITY;

CREATE POLICY agent_attachments_policy ON agent_attachments FOR ALL
    USING (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM agent_jobs j WHERE j.id = agent_attachments.job_id
        AND j.owner_user_id = agent_attachments.owner_user_id
        AND j.requester_user_id = agent_attachments.requester_user_id
        AND (j.owner_user_id = misty_rls_user_id() OR j.requester_user_id = misty_rls_user_id())
    ))
    WITH CHECK (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM agent_jobs j WHERE j.id = agent_attachments.job_id
        AND j.owner_user_id = agent_attachments.owner_user_id
        AND j.requester_user_id = agent_attachments.requester_user_id
        AND (j.owner_user_id = misty_rls_user_id() OR j.requester_user_id = misty_rls_user_id())
    ));
CREATE POLICY agent_artifacts_policy ON agent_artifacts FOR ALL
    USING (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM agent_jobs j WHERE j.id = agent_artifacts.job_id
        AND j.owner_user_id = agent_artifacts.owner_user_id
        AND (j.owner_user_id = misty_rls_user_id() OR j.requester_user_id = misty_rls_user_id())
    ))
    WITH CHECK (misty_rls_is_service() OR EXISTS (
        SELECT 1 FROM agent_jobs j WHERE j.id = agent_artifacts.job_id
        AND j.owner_user_id = agent_artifacts.owner_user_id
        AND j.owner_user_id = misty_rls_user_id()
    ));

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON agent_attachments, agent_artifacts TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_artifacts, agent_attachments CASCADE;
-- +goose StatementEnd
