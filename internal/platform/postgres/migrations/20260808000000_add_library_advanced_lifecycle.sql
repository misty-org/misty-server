-- +goose Up
-- +goose StatementBegin
CREATE TABLE space_library_grants (
    id TEXT PRIMARY KEY,
    source_space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    source_item_id TEXT NOT NULL REFERENCES space_library_items(id) ON DELETE CASCADE,
    destination_space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    granted_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    capabilities JSONB NOT NULL DEFAULT '["view"]'::jsonb,
    metadata_policy JSONB NOT NULL DEFAULT '{}'::jsonb,
    state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active','revoked','expired')),
    version BIGINT NOT NULL DEFAULT 1,
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (source_space_id<>destination_space_id)
);
CREATE INDEX space_library_grants_destination_idx ON space_library_grants(destination_space_id,state,created_at DESC);

CREATE TABLE space_library_direct_references (
    id TEXT PRIMARY KEY,
    destination_space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    grant_id TEXT NOT NULL REFERENCES space_library_grants(id) ON DELETE CASCADE,
    created_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    lifecycle_state TEXT NOT NULL DEFAULT 'ready' CHECK (lifecycle_state IN ('ready','unavailable','deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(destination_space_id,grant_id)
);

CREATE TABLE space_library_imports (
    id TEXT PRIMARY KEY,
    source_space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    source_item_id TEXT NOT NULL REFERENCES space_library_items(id) ON DELETE RESTRICT,
    source_security_domain_id TEXT NOT NULL REFERENCES security_domains(id) ON DELETE RESTRICT,
    destination_space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    destination_item_id TEXT REFERENCES space_library_items(id) ON DELETE RESTRICT,
    destination_security_domain_id TEXT NOT NULL REFERENCES security_domains(id) ON DELETE RESTRICT,
    importer_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    quota_reservation_upload_id TEXT REFERENCES space_library_uploads(id) ON DELETE RESTRICT,
    logical_bytes BIGINT NOT NULL CHECK (logical_bytes>0),
    copy_policy TEXT NOT NULL DEFAULT 'physical_destination_copy' CHECK (copy_policy IN ('physical_destination_copy')),
    state TEXT NOT NULL DEFAULT 'reserved' CHECK (state IN ('reserved','copying','processing','ready','failed','deleted')),
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);
CREATE INDEX space_library_imports_destination_idx ON space_library_imports(destination_space_id,created_at DESC);

CREATE TABLE library_recovery_tombstones (
    id TEXT PRIMARY KEY,
    security_domain_id TEXT NOT NULL REFERENCES security_domains(id) ON DELETE RESTRICT,
    space_id TEXT REFERENCES spaces(id) ON DELETE SET NULL,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('space_item','attachment','edit','file','blob','export','import')),
    target_id TEXT NOT NULL,
    lifecycle_state TEXT NOT NULL DEFAULT 'recovery' CHECK (lifecycle_state IN ('recovery','purging','purged','restored','held')),
    recover_until TIMESTAMPTZ NOT NULL,
    delete_lease_token TEXT,
    delete_lease_expires_at TIMESTAMPTZ,
    target_version BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(target_kind,target_id)
);
CREATE INDEX library_recovery_tombstones_due_idx ON library_recovery_tombstones(lifecycle_state,recover_until);

CREATE TABLE library_legal_holds (
    id TEXT PRIMARY KEY,
    security_domain_id TEXT NOT NULL REFERENCES security_domains(id) ON DELETE RESTRICT,
    space_id TEXT REFERENCES spaces(id) ON DELETE SET NULL,
    target_kind TEXT NOT NULL,
    target_id TEXT NOT NULL,
    reason_code TEXT NOT NULL,
    created_by_user_id TEXT REFERENCES users(id) ON DELETE RESTRICT,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    released_at TIMESTAMPTZ
);
CREATE INDEX library_legal_holds_target_idx ON library_legal_holds(target_kind,target_id) WHERE active;

CREATE TABLE library_exports (
    id TEXT PRIMARY KEY,
    security_domain_id TEXT NOT NULL REFERENCES security_domains(id) ON DELETE RESTRICT,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    requested_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    selection JSONB NOT NULL,
    export_blob_id TEXT REFERENCES library_blobs(id) ON DELETE RESTRICT,
    state TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','running','ready','failed','expired','deleted')),
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE library_processing_jobs (
    id TEXT PRIMARY KEY,
    security_domain_id TEXT NOT NULL REFERENCES security_domains(id) ON DELETE CASCADE,
    space_id TEXT REFERENCES spaces(id) ON DELETE CASCADE,
    job_kind TEXT NOT NULL CHECK (job_kind IN ('verify','scan','preview','ocr','metadata','ai','faces','media','edit','duplicates','export','retention','gc','quota_reconcile','r2_reconcile','import')),
    target_kind TEXT NOT NULL,
    target_id TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    priority SMALLINT NOT NULL DEFAULT 0,
    state TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','leased','running','completed','failed','dead','canceled')),
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5 CHECK (max_attempts BETWEEN 1 AND 20),
    lease_token TEXT,
    lease_owner TEXT,
    lease_expires_at TIMESTAMPTZ,
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(job_kind,target_kind,target_id)
);
CREATE INDEX library_processing_jobs_claim_idx ON library_processing_jobs(state,available_at,priority DESC,created_at);

ALTER TABLE space_library_grants ENABLE ROW LEVEL SECURITY; ALTER TABLE space_library_grants FORCE ROW LEVEL SECURITY;
CREATE POLICY space_library_grants_policy ON space_library_grants FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(source_space_id) OR misty_is_space_member(destination_space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(source_space_id));
ALTER TABLE space_library_direct_references ENABLE ROW LEVEL SECURITY; ALTER TABLE space_library_direct_references FORCE ROW LEVEL SECURITY;
CREATE POLICY direct_references_policy ON space_library_direct_references FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(destination_space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(destination_space_id));
ALTER TABLE space_library_imports ENABLE ROW LEVEL SECURITY; ALTER TABLE space_library_imports FORCE ROW LEVEL SECURITY;
CREATE POLICY space_library_imports_policy ON space_library_imports FOR SELECT USING (misty_rls_is_service() OR importer_user_id=misty_rls_user_id() AND misty_is_space_member(destination_space_id));
CREATE POLICY space_library_imports_insert ON space_library_imports FOR INSERT WITH CHECK (misty_rls_is_service() OR importer_user_id=misty_rls_user_id() AND misty_is_space_member(destination_space_id));
ALTER TABLE library_recovery_tombstones ENABLE ROW LEVEL SECURITY; ALTER TABLE library_recovery_tombstones FORCE ROW LEVEL SECURITY;
CREATE POLICY recovery_tombstones_policy ON library_recovery_tombstones FOR ALL USING (misty_rls_is_service() OR space_id IS NOT NULL AND misty_is_space_owner(space_id)) WITH CHECK (misty_rls_is_service() OR space_id IS NOT NULL AND misty_is_space_owner(space_id));
ALTER TABLE library_legal_holds ENABLE ROW LEVEL SECURITY; ALTER TABLE library_legal_holds FORCE ROW LEVEL SECURITY;
CREATE POLICY legal_holds_service_policy ON library_legal_holds FOR ALL USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
ALTER TABLE library_exports ENABLE ROW LEVEL SECURITY; ALTER TABLE library_exports FORCE ROW LEVEL SECURITY;
CREATE POLICY library_exports_policy ON library_exports FOR ALL USING (misty_rls_is_service() OR requested_by_user_id=misty_rls_user_id() AND misty_is_space_member(space_id)) WITH CHECK (misty_rls_is_service() OR requested_by_user_id=misty_rls_user_id() AND misty_is_space_member(space_id));
ALTER TABLE library_processing_jobs ENABLE ROW LEVEL SECURITY; ALTER TABLE library_processing_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY library_processing_jobs_service_policy ON library_processing_jobs FOR ALL USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());

DO $grant$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_library_grants,space_library_direct_references,space_library_imports,library_recovery_tombstones,library_legal_holds,library_exports,library_processing_jobs TO misty_app;
    END IF;
END $grant$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS library_processing_jobs,library_exports,library_legal_holds,library_recovery_tombstones,space_library_imports,space_library_direct_references,space_library_grants;
-- +goose StatementEnd
