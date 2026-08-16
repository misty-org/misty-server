-- +goose Up
-- +goose StatementBegin
-- A connected account belongs to a person, not a Space or a particular Misty
-- tool. Tool capabilities are granted incrementally and can later be bound to
-- a Space without duplicating the provider credential.
CREATE TABLE connected_accounts (
    id TEXT PRIMARY KEY CHECK (id ~ '^connection_[0-9a-f-]{36}$'),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('google','microsoft')),
    account_id TEXT NOT NULL CHECK (char_length(account_id) BETWEEN 1 AND 320),
    account_display TEXT NOT NULL DEFAULT '' CHECK (char_length(account_display) <= 320),
    credential_ciphertext BYTEA NOT NULL,
    credential_nonce BYTEA NOT NULL,
    key_version SMALLINT NOT NULL DEFAULT 1,
    capabilities JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(capabilities)='array'),
    granted_scopes JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(granted_scopes)='array'),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','needs_attention','revoked')),
    last_error_code TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ,
    last_refreshed_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id,provider,account_id)
);
CREATE INDEX connected_accounts_owner_idx
    ON connected_accounts(user_id,provider,status,updated_at DESC);

CREATE TABLE connected_account_oauth_states (
    state_hash TEXT PRIMARY KEY CHECK (state_hash ~ '^[0-9a-f]{64}$'),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('google','microsoft')),
    capabilities JSONB NOT NULL CHECK (jsonb_typeof(capabilities)='array'),
    requested_scopes JSONB NOT NULL CHECK (jsonb_typeof(requested_scopes)='array'),
    verifier_ciphertext BYTEA NOT NULL,
    verifier_nonce BYTEA NOT NULL,
    return_to TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX connected_account_oauth_states_expiry_idx
    ON connected_account_oauth_states(expires_at) WHERE consumed_at IS NULL;

ALTER TABLE connected_accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE connected_accounts FORCE ROW LEVEL SECURITY;
ALTER TABLE connected_account_oauth_states ENABLE ROW LEVEL SECURITY;
ALTER TABLE connected_account_oauth_states FORCE ROW LEVEL SECURITY;

CREATE POLICY connected_accounts_owner ON connected_accounts FOR ALL
    USING (misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());
CREATE POLICY connected_account_oauth_states_owner ON connected_account_oauth_states FOR ALL
    USING (misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON connected_accounts,connected_account_oauth_states TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: account credentials and consent history must not be silently
-- removed by a rollback. Restore a pre-migration backup to cross this boundary.
SELECT 1;
