-- +goose Up
-- +goose StatementBegin
-- GitHub is represented by an App installation, never a stored user OAuth
-- token. Installation access tokens are minted on demand and remain outside
-- PostgreSQL.
ALTER TABLE provider_shared_resources DROP CONSTRAINT IF EXISTS provider_shared_resources_provider_check;
ALTER TABLE provider_shared_resources DROP CONSTRAINT IF EXISTS provider_shared_resources_resource_type_check;
ALTER TABLE provider_shared_resources ADD CONSTRAINT provider_shared_resources_provider_check
    CHECK (provider ~ '^[a-z][a-z0-9_]{1,39}$');
ALTER TABLE provider_shared_resources ADD CONSTRAINT provider_shared_resources_resource_type_check
    CHECK (resource_type ~ '^[a-z][a-z0-9_]{1,39}$');
ALTER TABLE provider_content_records DROP CONSTRAINT IF EXISTS provider_content_records_provider_check;
ALTER TABLE provider_content_records ADD CONSTRAINT provider_content_records_provider_check
    CHECK (provider ~ '^[a-z][a-z0-9_]{1,39}$');

CREATE TABLE github_app_setup_states (
    state_hash TEXT PRIMARY KEY CHECK (state_hash ~ '^[0-9a-f]{64}$'),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    return_to TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX github_app_setup_states_expiry_idx ON github_app_setup_states(expires_at)
    WHERE consumed_at IS NULL;

CREATE TABLE github_app_installations (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    integration_id TEXT NOT NULL UNIQUE REFERENCES space_integrations(id) ON DELETE CASCADE,
    installed_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    installation_id BIGINT NOT NULL,
    account_id BIGINT NOT NULL,
    account_login TEXT NOT NULL,
    account_type TEXT NOT NULL CHECK (account_type IN ('User','Organization','Enterprise','Bot')),
    repository_selection TEXT NOT NULL DEFAULT 'selected' CHECK (repository_selection IN ('all','selected')),
    permissions JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(permissions)='object'),
    events JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(events)='array'),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','needs_attention','disabled')),
    last_error_code TEXT NOT NULL DEFAULT '',
    suspended_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(space_id,installation_id)
);
CREATE INDEX github_app_installations_space_idx ON github_app_installations(space_id,status,account_login);

CREATE TABLE github_code_workspaces (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    installation_id TEXT NOT NULL REFERENCES github_app_installations(id) ON DELETE CASCADE,
    shared_resource_id TEXT NOT NULL UNIQUE REFERENCES provider_shared_resources(id) ON DELETE CASCADE,
    bound_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    repository_id BIGINT NOT NULL,
    full_name TEXT NOT NULL,
    default_branch TEXT NOT NULL DEFAULT '',
    clone_url TEXT NOT NULL,
    html_url TEXT NOT NULL,
    private BOOLEAN NOT NULL DEFAULT FALSE,
    client_workspace_id TEXT NOT NULL DEFAULT '',
    permissions JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(permissions)='object'),
    sync_cursor TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('pending','active','needs_attention','disabled')),
    last_error_code TEXT NOT NULL DEFAULT '',
    last_synced_at TIMESTAMPTZ,
    disabled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(space_id,repository_id),
    CHECK (client_workspace_id='' OR char_length(client_workspace_id) BETWEEN 8 AND 200)
);
CREATE INDEX github_code_workspaces_space_idx ON github_code_workspaces(space_id,status,full_name);

CREATE TABLE github_repository_records (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES github_code_workspaces(id) ON DELETE CASCADE,
    repository_id BIGINT NOT NULL,
    record_type TEXT NOT NULL CHECK (record_type IN ('repository','branch','commit','issue','pull_request')),
    external_id TEXT NOT NULL,
    parent_external_id TEXT NOT NULL DEFAULT '',
    ref_name TEXT NOT NULL DEFAULT '',
    sha TEXT NOT NULL DEFAULT '',
    number BIGINT,
    state TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    actor_login TEXT NOT NULL DEFAULT '',
    fingerprint TEXT NOT NULL,
    provenance JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(provenance)='object'),
    occurred_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(workspace_id,record_type,external_id)
);
CREATE INDEX github_repository_records_query_idx ON github_repository_records(space_id,workspace_id,record_type,occurred_at DESC,id)
    WHERE deleted_at IS NULL;

CREATE TABLE github_webhook_deliveries (
    delivery_id TEXT PRIMARY KEY,
    event_name TEXT NOT NULL,
    action TEXT NOT NULL DEFAULT '',
    installation_id BIGINT,
    repository_id BIGINT,
    payload_sha256 TEXT NOT NULL CHECK (payload_sha256 ~ '^[0-9a-f]{64}$'),
    state TEXT NOT NULL DEFAULT 'processing' CHECK (state IN ('processing','processed','ignored','failed')),
    error_code TEXT NOT NULL DEFAULT '',
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ
);

CREATE TABLE github_credential_handoffs (
    handle_hash TEXT PRIMARY KEY CHECK (handle_hash ~ '^[0-9a-f]{64}$'),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES github_code_workspaces(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX github_credential_handoffs_expiry_idx ON github_credential_handoffs(expires_at)
    WHERE consumed_at IS NULL;

CREATE TABLE github_mutation_audit (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES github_code_workspaces(id) ON DELETE CASCADE,
    actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    source TEXT NOT NULL CHECK (source IN ('user','agent')),
    operation TEXT NOT NULL CHECK (operation IN ('create_issue','comment_issue','create_branch','create_pull_request')),
    confirmed BOOLEAN NOT NULL,
    success BOOLEAN NOT NULL,
    target_ref TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX github_mutation_audit_space_idx ON github_mutation_audit(space_id,created_at DESC);

ALTER TABLE github_app_setup_states ENABLE ROW LEVEL SECURITY; ALTER TABLE github_app_setup_states FORCE ROW LEVEL SECURITY;
ALTER TABLE github_app_installations ENABLE ROW LEVEL SECURITY; ALTER TABLE github_app_installations FORCE ROW LEVEL SECURITY;
ALTER TABLE github_code_workspaces ENABLE ROW LEVEL SECURITY; ALTER TABLE github_code_workspaces FORCE ROW LEVEL SECURITY;
ALTER TABLE github_repository_records ENABLE ROW LEVEL SECURITY; ALTER TABLE github_repository_records FORCE ROW LEVEL SECURITY;
ALTER TABLE github_webhook_deliveries ENABLE ROW LEVEL SECURITY; ALTER TABLE github_webhook_deliveries FORCE ROW LEVEL SECURITY;
ALTER TABLE github_credential_handoffs ENABLE ROW LEVEL SECURITY; ALTER TABLE github_credential_handoffs FORCE ROW LEVEL SECURITY;
ALTER TABLE github_mutation_audit ENABLE ROW LEVEL SECURITY; ALTER TABLE github_mutation_audit FORCE ROW LEVEL SECURITY;

CREATE POLICY github_app_setup_states_owner ON github_app_setup_states FOR ALL USING (misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());
CREATE POLICY github_app_installations_member ON github_app_installations FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY github_code_workspaces_member ON github_code_workspaces FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY github_repository_records_member ON github_repository_records FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY github_webhook_deliveries_service ON github_webhook_deliveries FOR ALL USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
CREATE POLICY github_credential_handoffs_owner ON github_credential_handoffs FOR ALL USING (misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());
CREATE POLICY github_mutation_audit_member ON github_mutation_audit FOR SELECT USING (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY github_mutation_audit_service_write ON github_mutation_audit FOR INSERT WITH CHECK (misty_rls_is_service());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON github_app_setup_states,github_app_installations,
            github_code_workspaces,github_repository_records,github_webhook_deliveries,github_credential_handoffs,
            github_mutation_audit TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only: installation and repository provenance must never be erased by
-- a rollback command.
SELECT 1;
-- +goose StatementEnd
