-- +goose Up
-- +goose StatementBegin
-- Per-user provider credentials and callback state for the v2 Agent runtime.
-- Secret material is encrypted by the API before it reaches PostgreSQL.
DELETE FROM library_derivatives WHERE kind='ocr';
ALTER TABLE space_library_intelligence_policies DROP COLUMN IF EXISTS ocr_enabled;
ALTER TABLE space_integrations DROP CONSTRAINT IF EXISTS space_integrations_space_id_provider_display_name_key;
ALTER TABLE space_integrations ADD CONSTRAINT space_integrations_private_account_key UNIQUE(space_id,connected_by_user_id,provider,display_name);
CREATE TABLE space_provider_credentials (
    id TEXT PRIMARY KEY,
    integration_id TEXT NOT NULL UNIQUE REFERENCES space_integrations(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    ciphertext BYTEA NOT NULL,
    nonce BYTEA NOT NULL,
    key_version SMALLINT NOT NULL DEFAULT 1,
    account_id TEXT NOT NULL DEFAULT '',
    account_display TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ,
    last_refreshed_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(space_id,user_id,provider,account_id)
);
CREATE INDEX space_provider_credentials_owner_idx ON space_provider_credentials(user_id,space_id,provider);

-- Preserve connection records and user bindings, but legacy opaque references
-- cannot be imported into the encrypted v2 vault. They remain visible as
-- attention items until the owner completes the branded consent flow.
UPDATE space_integrations
SET status='needs_attention',credential_reference='reauthorization_required',updated_at=NOW()
WHERE status='active';

CREATE TABLE provider_oauth_states (
    state_hash TEXT PRIMARY KEY CHECK (state_hash ~ '^[0-9a-f]{64}$'),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    verifier_ciphertext BYTEA NOT NULL,
    verifier_nonce BYTEA NOT NULL,
    return_to TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX provider_oauth_states_expiry_idx ON provider_oauth_states(expires_at) WHERE consumed_at IS NULL;

CREATE TABLE provider_subscriptions (
    id TEXT PRIMARY KEY,
    integration_id TEXT NOT NULL REFERENCES space_integrations(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    resource_key TEXT NOT NULL,
    external_subscription_id TEXT NOT NULL,
    cursor JSONB NOT NULL DEFAULT '{}'::jsonb,
    expires_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','renewing','needs_attention','disabled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(integration_id,resource_key)
);

CREATE TABLE provider_event_inbox (
    id BIGSERIAL PRIMARY KEY,
    integration_id TEXT NOT NULL REFERENCES space_integrations(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    external_event_id TEXT NOT NULL,
    payload JSONB NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','claimed','processed','failed')),
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ,
    UNIQUE(integration_id,external_event_id)
);

-- Exact device jobs replace leasing whole Agent requests. A completion may be
-- submitted more than once, but only the active lease token can win.
CREATE TABLE workflow_device_node_jobs (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES space_runs(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL,
    attempt INTEGER NOT NULL CHECK (attempt BETWEEN 1 AND 3),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope_id TEXT NOT NULL,
    operation TEXT NOT NULL,
    input JSONB NOT NULL,
    config JSONB NOT NULL,
    input_schema JSONB NOT NULL DEFAULT '{}'::jsonb,
    output_schema JSONB NOT NULL DEFAULT '{}'::jsonb,
    state TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','leased','completed','failed','canceled')),
    leased_device_id TEXT,
    lease_token_hash TEXT,
    lease_expires_at TIMESTAMPTZ,
    last_heartbeat_at TIMESTAMPTZ,
    output JSONB,
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    UNIQUE(run_id,node_id,attempt)
);
CREATE INDEX workflow_device_node_jobs_claim_idx ON workflow_device_node_jobs(user_id,state,created_at);

ALTER TABLE space_provider_credentials ENABLE ROW LEVEL SECURITY; ALTER TABLE space_provider_credentials FORCE ROW LEVEL SECURITY;
ALTER TABLE provider_oauth_states ENABLE ROW LEVEL SECURITY; ALTER TABLE provider_oauth_states FORCE ROW LEVEL SECURITY;
ALTER TABLE provider_subscriptions ENABLE ROW LEVEL SECURITY; ALTER TABLE provider_subscriptions FORCE ROW LEVEL SECURITY;
ALTER TABLE provider_event_inbox ENABLE ROW LEVEL SECURITY; ALTER TABLE provider_event_inbox FORCE ROW LEVEL SECURITY;
ALTER TABLE workflow_device_node_jobs ENABLE ROW LEVEL SECURITY; ALTER TABLE workflow_device_node_jobs FORCE ROW LEVEL SECURITY;

CREATE POLICY space_provider_credentials_owner ON space_provider_credentials FOR ALL USING (misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());
CREATE POLICY provider_oauth_states_owner ON provider_oauth_states FOR ALL USING (misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());
CREATE POLICY provider_subscriptions_owner ON provider_subscriptions FOR ALL USING (misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());
CREATE POLICY provider_event_inbox_owner ON provider_event_inbox FOR ALL USING (misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());
CREATE POLICY workflow_device_node_jobs_owner ON workflow_device_node_jobs FOR ALL USING (misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_provider_credentials,provider_oauth_states,provider_subscriptions,provider_event_inbox,workflow_device_node_jobs TO misty_app;
        GRANT USAGE,SELECT ON SEQUENCE provider_event_inbox_id_seq TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS workflow_device_node_jobs,provider_event_inbox,provider_subscriptions,provider_oauth_states,space_provider_credentials CASCADE;
ALTER TABLE space_library_intelligence_policies ADD COLUMN IF NOT EXISTS ocr_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE space_integrations DROP CONSTRAINT IF EXISTS space_integrations_private_account_key;
-- +goose StatementEnd
