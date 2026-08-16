-- +goose Up
-- +goose StatementBegin
CREATE TABLE figma_space_bindings (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    connection_id TEXT NOT NULL REFERENCES connected_accounts(id) ON DELETE CASCADE,
    integration_id TEXT NOT NULL REFERENCES space_integrations(id) ON DELETE CASCADE,
    shared_resource_id TEXT NOT NULL UNIQUE REFERENCES provider_shared_resources(id) ON DELETE CASCADE,
    bound_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    resource_type TEXT NOT NULL CHECK (resource_type IN ('file','project')),
    external_id TEXT NOT NULL,
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 240),
    team_id TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL DEFAULT '',
    file_key TEXT NOT NULL DEFAULT '',
    sync_cursor TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','active','needs_attention','disabled')),
    last_error_code TEXT NOT NULL DEFAULT '',
    last_synced_at TIMESTAMPTZ,
    disabled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(space_id,resource_type,external_id),
    CHECK ((resource_type='file' AND file_key=external_id AND project_id='') OR
           (resource_type='project' AND project_id=external_id AND file_key=''))
);
CREATE INDEX figma_space_bindings_space_idx ON figma_space_bindings(space_id,status,display_name);

CREATE TABLE figma_webhook_subscriptions (
    id TEXT PRIMARY KEY,
    binding_id TEXT NOT NULL REFERENCES figma_space_bindings(id) ON DELETE CASCADE,
    webhook_id TEXT NOT NULL UNIQUE,
    event_type TEXT NOT NULL CHECK (event_type IN ('FILE_UPDATE','FILE_VERSION_UPDATE','FILE_COMMENT')),
    passcode_hash TEXT NOT NULL CHECK (passcode_hash ~ '^[0-9a-f]{64}$'),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','needs_attention','disabled')),
    last_error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(binding_id,event_type)
);

CREATE TABLE figma_content_records (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    binding_id TEXT NOT NULL REFERENCES figma_space_bindings(id) ON DELETE CASCADE,
    file_key TEXT NOT NULL,
    record_type TEXT NOT NULL CHECK (record_type IN ('file','version','comment','webhook_event')),
    external_id TEXT NOT NULL,
    parent_external_id TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    actor_id TEXT NOT NULL DEFAULT '',
    actor_name TEXT NOT NULL DEFAULT '',
    resolved BOOLEAN,
    fingerprint TEXT NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    provenance JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(provenance)='object'),
    occurred_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(binding_id,record_type,external_id)
);
CREATE INDEX figma_content_records_query_idx ON figma_content_records(space_id,binding_id,record_type,occurred_at DESC,id) WHERE deleted_at IS NULL;

CREATE TABLE figma_webhook_deliveries (
    delivery_hash TEXT PRIMARY KEY CHECK (delivery_hash ~ '^[0-9a-f]{64}$'),
    subscription_id TEXT NOT NULL REFERENCES figma_webhook_subscriptions(id) ON DELETE CASCADE,
    webhook_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    file_key TEXT NOT NULL DEFAULT '',
    event_timestamp TIMESTAMPTZ,
    state TEXT NOT NULL DEFAULT 'processing' CHECK (state IN ('processing','processed','ignored','failed')),
    error_code TEXT NOT NULL DEFAULT '',
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ
);

CREATE TABLE figma_comment_audit (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    binding_id TEXT NOT NULL REFERENCES figma_space_bindings(id) ON DELETE CASCADE,
    actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    source TEXT NOT NULL CHECK (source IN ('user','agent')),
	 idempotency_key TEXT NOT NULL DEFAULT '',
	 action_fingerprint TEXT NOT NULL DEFAULT '',
    file_key TEXT NOT NULL,
    target_node_id TEXT NOT NULL DEFAULT '',
    confirmed BOOLEAN NOT NULL,
    success BOOLEAN NOT NULL,
    error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX figma_comment_audit_space_idx ON figma_comment_audit(space_id,created_at DESC);
CREATE UNIQUE INDEX figma_comment_audit_idempotency_idx ON figma_comment_audit(binding_id,idempotency_key) WHERE idempotency_key<>'';

ALTER TABLE figma_space_bindings ENABLE ROW LEVEL SECURITY; ALTER TABLE figma_space_bindings FORCE ROW LEVEL SECURITY;
ALTER TABLE figma_webhook_subscriptions ENABLE ROW LEVEL SECURITY; ALTER TABLE figma_webhook_subscriptions FORCE ROW LEVEL SECURITY;
ALTER TABLE figma_content_records ENABLE ROW LEVEL SECURITY; ALTER TABLE figma_content_records FORCE ROW LEVEL SECURITY;
ALTER TABLE figma_webhook_deliveries ENABLE ROW LEVEL SECURITY; ALTER TABLE figma_webhook_deliveries FORCE ROW LEVEL SECURITY;
ALTER TABLE figma_comment_audit ENABLE ROW LEVEL SECURITY; ALTER TABLE figma_comment_audit FORCE ROW LEVEL SECURITY;

CREATE POLICY figma_space_bindings_member_read ON figma_space_bindings FOR SELECT USING (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY figma_space_bindings_service_write ON figma_space_bindings FOR ALL USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
CREATE POLICY figma_webhook_subscriptions_member_read ON figma_webhook_subscriptions FOR SELECT USING (misty_rls_is_service() OR EXISTS(SELECT 1 FROM figma_space_bindings b WHERE b.id=binding_id AND misty_is_space_member(b.space_id)));
CREATE POLICY figma_webhook_subscriptions_service_write ON figma_webhook_subscriptions FOR ALL USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
CREATE POLICY figma_content_records_member_read ON figma_content_records FOR SELECT USING (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY figma_content_records_service_write ON figma_content_records FOR ALL USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
CREATE POLICY figma_webhook_deliveries_service ON figma_webhook_deliveries FOR ALL USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
CREATE POLICY figma_comment_audit_member_read ON figma_comment_audit FOR SELECT USING (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY figma_comment_audit_service_write ON figma_comment_audit FOR ALL USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON figma_space_bindings,figma_webhook_subscriptions,
            figma_content_records,figma_webhook_deliveries,figma_comment_audit TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only: Figma binding, provenance, webhook, and audit history must not
-- be erased by an application rollback.
SELECT 1;
-- +goose StatementEnd
