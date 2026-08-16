-- +goose Up
-- +goose StatementBegin
ALTER TABLE connected_accounts DROP CONSTRAINT connected_accounts_provider_check;
ALTER TABLE connected_accounts ADD CONSTRAINT connected_accounts_provider_check
    CHECK (provider ~ '^[a-z][a-z0-9_]{1,31}$');
ALTER TABLE connected_account_oauth_states DROP CONSTRAINT connected_account_oauth_states_provider_check;
ALTER TABLE connected_account_oauth_states ADD CONSTRAINT connected_account_oauth_states_provider_check
    CHECK (provider ~ '^[a-z][a-z0-9_]{1,31}$');

ALTER TABLE cloud_connections
    ADD COLUMN connected_account_id TEXT REFERENCES connected_accounts(id) ON DELETE SET NULL,
    ADD COLUMN status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','needs_attention','revoked')),
    ADD COLUMN last_error_code TEXT NOT NULL DEFAULT '';
CREATE INDEX cloud_connections_connected_account_idx
    ON cloud_connections(connected_account_id) WHERE revoked_at IS NULL;

-- Non-destructive compatibility bridge: existing cloud credentials continue to
-- work, while an already-unified account with the same provider identity is
-- linked and becomes the refresh authority.
UPDATE cloud_connections cloud SET connected_account_id=account.id
FROM connected_accounts account
WHERE cloud.user_id=account.user_id AND cloud.account_id=account.account_id
  AND account.revoked_at IS NULL
  AND ((cloud.provider='drive' AND account.provider='google')
    OR (cloud.provider='onedrive' AND account.provider='microsoft')
    OR (cloud.provider='dropbox' AND account.provider='dropbox'));

CREATE TABLE cloud_credential_handoffs (
    handoff_hash TEXT PRIMARY KEY CHECK (handoff_hash ~ '^[0-9a-f]{64}$'),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    cloud_connection_id TEXT NOT NULL REFERENCES cloud_connections(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX cloud_credential_handoffs_expiry_idx
    ON cloud_credential_handoffs(expires_at) WHERE consumed_at IS NULL;
ALTER TABLE cloud_credential_handoffs ENABLE ROW LEVEL SECURITY;
ALTER TABLE cloud_credential_handoffs FORCE ROW LEVEL SECURITY;
CREATE POLICY cloud_credential_handoffs_owner ON cloud_credential_handoffs FOR ALL
    USING (misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON cloud_credential_handoffs TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only: account bindings and consumed one-time handoffs are security
-- history. Restore a pre-migration backup to cross this boundary.
SELECT 1;
-- +goose StatementEnd
