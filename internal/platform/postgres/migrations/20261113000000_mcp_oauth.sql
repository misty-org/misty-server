-- +goose Up
-- +goose StatementBegin
ALTER TABLE mcp_remote_connections
    ADD COLUMN provider TEXT NOT NULL DEFAULT 'custom'
    CHECK (provider IN ('custom','activepieces'));

CREATE TABLE mcp_oauth_credentials (
    connection_id TEXT PRIMARY KEY REFERENCES mcp_remote_connections(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_ciphertext BYTEA NOT NULL,
    credential_nonce BYTEA NOT NULL,
    key_version SMALLINT NOT NULL DEFAULT 1 CHECK (key_version>0),
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX mcp_oauth_credentials_owner_idx ON mcp_oauth_credentials(owner_user_id,updated_at DESC);

CREATE TABLE mcp_oauth_states (
    state_hash TEXT PRIMARY KEY CHECK (state_hash ~ '^[0-9a-f]{64}$'),
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    secret_ciphertext BYTEA NOT NULL,
    secret_nonce BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX mcp_oauth_states_expiry_idx ON mcp_oauth_states(expires_at) WHERE consumed_at IS NULL;

ALTER TABLE mcp_oauth_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE mcp_oauth_credentials FORCE ROW LEVEL SECURITY;
ALTER TABLE mcp_oauth_states ENABLE ROW LEVEL SECURITY;
ALTER TABLE mcp_oauth_states FORCE ROW LEVEL SECURITY;

CREATE POLICY mcp_oauth_credentials_owner ON mcp_oauth_credentials FOR ALL
    USING (misty_rls_is_service() OR owner_user_id=misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id=misty_rls_user_id());
CREATE POLICY mcp_oauth_states_owner ON mcp_oauth_states FOR ALL
    USING (misty_rls_is_service() OR owner_user_id=misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id=misty_rls_user_id());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON mcp_oauth_credentials,mcp_oauth_states TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: connector credentials and authorization state must not be
-- silently discarded by a rollback. Restore a pre-migration backup instead.
SELECT 1;
