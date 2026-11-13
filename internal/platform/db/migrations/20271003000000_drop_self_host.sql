-- +goose Up
-- Misty is hosted only. Self-host accounts, enrollment, bootstrap tokens and
-- the local collaboration store have no remaining readers or writers.
DROP TABLE self_host_enrollment_invitations;
DROP TABLE self_host_bootstrap_tokens;
DROP TABLE self_host_collaboration_documents;
DROP TABLE self_host_accounts;

-- +goose Down
CREATE TABLE self_host_accounts (
    user_id text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    entitlement_subject text NOT NULL UNIQUE CHECK (char_length(entitlement_subject) BETWEEN 16 AND 160),
    entitlement_expires_at timestamptz NOT NULL,
    is_admin boolean NOT NULL DEFAULT false,
    disabled_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE self_host_bootstrap_tokens (
    token_hash text PRIMARY KEY CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at <= created_at + interval '30 minutes 5 seconds')
);
CREATE TABLE self_host_collaboration_documents (
    resource_type text NOT NULL CHECK (resource_type IN ('note', 'drawing')),
    resource_id text NOT NULL CHECK (char_length(resource_id) BETWEEN 1 AND 200),
    state bytea NOT NULL CHECK (octet_length(state) <= 8388608),
    checksum_sha256 text NOT NULL CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$'),
    acl_version bigint NOT NULL DEFAULT 0 CHECK (acl_version >= 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (resource_type, resource_id)
);
CREATE TABLE self_host_enrollment_invitations (
    id text PRIMARY KEY CHECK (id ~ '^enrollment_[0-9a-f-]{36}$'),
    token_hash text NOT NULL UNIQUE CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    created_by text NOT NULL REFERENCES self_host_accounts(user_id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    consumed_by text REFERENCES self_host_accounts(user_id) ON DELETE SET NULL,
    consumed_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at <= created_at + interval '7 days 5 seconds')
);
ALTER TABLE self_host_accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE self_host_accounts FORCE ROW LEVEL SECURITY;
CREATE POLICY self_host_accounts_service ON self_host_accounts
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());
ALTER TABLE self_host_bootstrap_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE self_host_bootstrap_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY self_host_bootstrap_tokens_service ON self_host_bootstrap_tokens
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
ALTER TABLE self_host_collaboration_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE self_host_collaboration_documents FORCE ROW LEVEL SECURITY;
CREATE POLICY self_host_collaboration_documents_service ON self_host_collaboration_documents
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
ALTER TABLE self_host_enrollment_invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE self_host_enrollment_invitations FORCE ROW LEVEL SECURITY;
CREATE POLICY self_host_enrollment_invitations_service ON self_host_enrollment_invitations
    USING (misty_rls_is_service() OR created_by = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR created_by = misty_rls_user_id());
GRANT SELECT, INSERT, DELETE, UPDATE ON self_host_accounts, self_host_bootstrap_tokens,
    self_host_collaboration_documents, self_host_enrollment_invitations TO misty_app;
