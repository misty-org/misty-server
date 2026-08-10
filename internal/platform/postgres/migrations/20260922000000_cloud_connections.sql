-- +goose Up
-- +goose StatementBegin
CREATE TABLE cloud_connections (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('drive','dropbox','onedrive')),
    name TEXT NOT NULL,
    account_id TEXT NOT NULL,
    account_display TEXT NOT NULL DEFAULT '',
    credential_ciphertext BYTEA NOT NULL,
    credential_nonce BYTEA NOT NULL,
    key_version SMALLINT NOT NULL DEFAULT 1,
    uses_custom_oauth_client BOOLEAN NOT NULL DEFAULT FALSE,
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, provider, account_id),
    UNIQUE(user_id, name)
);
CREATE INDEX cloud_connections_user_idx ON cloud_connections(user_id) WHERE revoked_at IS NULL;

CREATE TABLE cloud_oauth_states (
    state_hash TEXT PRIMARY KEY CHECK (state_hash ~ '^[0-9a-f]{64}$'),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('drive','dropbox','onedrive')),
    connection_name TEXT NOT NULL,
    secret_ciphertext BYTEA NOT NULL,
    secret_nonce BYTEA NOT NULL,
    return_to TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX cloud_oauth_states_expiry_idx ON cloud_oauth_states(expires_at) WHERE consumed_at IS NULL;

ALTER TABLE cloud_connections ENABLE ROW LEVEL SECURITY;
ALTER TABLE cloud_connections FORCE ROW LEVEL SECURITY;
ALTER TABLE cloud_oauth_states ENABLE ROW LEVEL SECURITY;
ALTER TABLE cloud_oauth_states FORCE ROW LEVEL SECURITY;
CREATE POLICY cloud_connections_owner ON cloud_connections FOR ALL
    USING (misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());
CREATE POLICY cloud_oauth_states_owner ON cloud_oauth_states FOR ALL
    USING (misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON cloud_connections,cloud_oauth_states TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS cloud_oauth_states,cloud_connections CASCADE;
-- +goose StatementEnd
