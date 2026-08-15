-- +goose Up
-- +goose StatementBegin
CREATE TABLE misty_instance (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    server_id TEXT NOT NULL UNIQUE CHECK (server_id ~ '^server_[0-9a-f-]{36}$'),
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 120),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE self_host_accounts (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    entitlement_subject TEXT NOT NULL UNIQUE CHECK (char_length(entitlement_subject) BETWEEN 16 AND 160),
    entitlement_expires_at TIMESTAMPTZ NOT NULL,
    is_admin BOOLEAN NOT NULL DEFAULT FALSE,
    disabled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE self_host_bootstrap_tokens (
    token_hash TEXT PRIMARY KEY CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (expires_at <= created_at + INTERVAL '30 minutes 5 seconds')
);

CREATE TABLE self_host_enrollment_invitations (
    id TEXT PRIMARY KEY CHECK (id ~ '^enrollment_[0-9a-f-]{36}$'),
    token_hash TEXT NOT NULL UNIQUE CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    created_by TEXT NOT NULL REFERENCES self_host_accounts(user_id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_by TEXT REFERENCES self_host_accounts(user_id) ON DELETE SET NULL,
    consumed_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (expires_at <= created_at + INTERVAL '7 days 5 seconds')
);

CREATE TABLE self_host_collaboration_documents (
    resource_type TEXT NOT NULL CHECK (resource_type IN ('note','drawing')),
    resource_id TEXT NOT NULL CHECK (char_length(resource_id) BETWEEN 1 AND 200),
    state BYTEA NOT NULL CHECK (octet_length(state) <= 8388608),
    checksum_sha256 TEXT NOT NULL CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$'),
    acl_version BIGINT NOT NULL DEFAULT 0 CHECK (acl_version >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (resource_type, resource_id)
);

ALTER TABLE self_host_accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE self_host_accounts FORCE ROW LEVEL SECURITY;
ALTER TABLE self_host_bootstrap_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE self_host_bootstrap_tokens FORCE ROW LEVEL SECURITY;
ALTER TABLE self_host_enrollment_invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE self_host_enrollment_invitations FORCE ROW LEVEL SECURITY;
ALTER TABLE self_host_collaboration_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE self_host_collaboration_documents FORCE ROW LEVEL SECURITY;

CREATE POLICY self_host_accounts_service ON self_host_accounts FOR ALL
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());
CREATE POLICY self_host_bootstrap_tokens_service ON self_host_bootstrap_tokens FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
CREATE POLICY self_host_enrollment_invitations_service ON self_host_enrollment_invitations FOR ALL
    USING (misty_rls_is_service() OR created_by = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR created_by = misty_rls_user_id());
CREATE POLICY self_host_collaboration_documents_service ON self_host_collaboration_documents FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE ON misty_instance TO misty_app;
        GRANT SELECT, INSERT, UPDATE, DELETE ON self_host_accounts,
            self_host_bootstrap_tokens, self_host_enrollment_invitations,
            self_host_collaboration_documents TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS self_host_collaboration_documents, self_host_enrollment_invitations, self_host_bootstrap_tokens,
    self_host_accounts, misty_instance;
-- +goose StatementEnd
