-- +goose Up
-- +goose StatementBegin
DROP INDEX IF EXISTS spaces_one_additional_per_user_idx;
CREATE INDEX IF NOT EXISTS spaces_owner_idx ON spaces(owner_user_id, created_at);

CREATE TABLE security_domains (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('personal', 'space', 'organization')),
    owner_user_id TEXT REFERENCES users(id) ON DELETE RESTRICT,
    space_id TEXT,
    lifecycle_state TEXT NOT NULL DEFAULT 'active' CHECK (lifecycle_state IN ('active', 'suspended', 'deleted')),
    policy JSONB NOT NULL DEFAULT '{}'::jsonb,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (
        (kind='personal' AND owner_user_id IS NOT NULL AND space_id IS NULL) OR
        (kind='space' AND owner_user_id IS NOT NULL AND space_id IS NOT NULL) OR
        (kind='organization')
    )
);
CREATE UNIQUE INDEX security_domains_personal_owner_idx ON security_domains(owner_user_id) WHERE kind='personal';
CREATE UNIQUE INDEX security_domains_space_idx ON security_domains(space_id) WHERE kind='space';

ALTER TABLE spaces ADD COLUMN security_domain_id TEXT;

INSERT INTO security_domains(id,kind,owner_user_id)
SELECT 'sd_personal_'||md5(owner_user_id),'personal',owner_user_id
FROM spaces WHERE is_personal
ON CONFLICT DO NOTHING;

UPDATE spaces s SET security_domain_id=d.id
FROM security_domains d
WHERE s.is_personal AND d.kind='personal' AND d.owner_user_id=s.owner_user_id;

INSERT INTO security_domains(id,kind,owner_user_id,space_id)
SELECT 'sd_space_'||md5(id),'space',owner_user_id,id
FROM spaces WHERE NOT is_personal
ON CONFLICT DO NOTHING;

UPDATE spaces s SET security_domain_id=d.id
FROM security_domains d
WHERE NOT s.is_personal AND d.kind='space' AND d.space_id=s.id;

ALTER TABLE spaces ALTER COLUMN security_domain_id SET NOT NULL;
ALTER TABLE security_domains ADD CONSTRAINT security_domains_space_fk
    FOREIGN KEY(space_id) REFERENCES spaces(id) ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE spaces ADD CONSTRAINT spaces_security_domain_fk
    FOREIGN KEY(security_domain_id) REFERENCES security_domains(id) ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX spaces_security_domain_idx ON spaces(security_domain_id);

CREATE TABLE library_blobs (
    id TEXT PRIMARY KEY,
    security_domain_id TEXT NOT NULL REFERENCES security_domains(id) ON DELETE RESTRICT,
    r2_object_key TEXT NOT NULL UNIQUE,
    sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    byte_size BIGINT NOT NULL CHECK (byte_size > 0),
    client_declared_mime_type TEXT NOT NULL DEFAULT '',
    server_detected_mime_type TEXT NOT NULL DEFAULT '',
    scan_status TEXT NOT NULL DEFAULT 'pending' CHECK (scan_status IN ('pending','clean','infected','failed','skipped')),
    processing_status TEXT NOT NULL DEFAULT 'pending' CHECK (processing_status IN ('pending','processing','ready','failed')),
    lifecycle_state TEXT NOT NULL DEFAULT 'quarantined' CHECK (lifecycle_state IN ('quarantined','ready','trash','purging','deleted','rejected','infected','invalid')),
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX library_blobs_domain_digest_idx ON library_blobs(security_domain_id,sha256,byte_size) WHERE lifecycle_state<>'deleted';

CREATE TABLE library_files (
    id TEXT PRIMARY KEY,
    blob_id TEXT NOT NULL REFERENCES library_blobs(id) ON DELETE RESTRICT,
    security_domain_id TEXT NOT NULL REFERENCES security_domains(id) ON DELETE RESTRICT,
    uploader_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    original_filename TEXT NOT NULL CHECK (char_length(original_filename) BETWEEN 1 AND 255),
    intrinsic_metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    lifecycle_state TEXT NOT NULL DEFAULT 'quarantined' CHECK (lifecycle_state IN ('quarantined','ready','trash','purging','deleted','rejected','infected','invalid')),
    original_uploaded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX library_files_blob_idx ON library_files(blob_id);
CREATE INDEX library_files_domain_idx ON library_files(security_domain_id,created_at DESC);

CREATE TABLE space_library_items (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    file_id TEXT NOT NULL REFERENCES library_files(id) ON DELETE RESTRICT,
    contributing_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 255),
    caption TEXT NOT NULL DEFAULT '' CHECK (char_length(caption) <= 4000),
    tags JSONB NOT NULL DEFAULT '[]'::jsonb,
    favorite BOOLEAN NOT NULL DEFAULT FALSE,
    hidden BOOLEAN NOT NULL DEFAULT FALSE,
    date_override TIMESTAMPTZ,
    location_override JSONB,
    contributor_information JSONB NOT NULL DEFAULT '{}'::jsonb,
    current_edit_version_id TEXT,
    added_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    lifecycle_state TEXT NOT NULL DEFAULT 'ready' CHECK (lifecycle_state IN ('ready','trash','purging','deleted')),
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    trashed_at TIMESTAMPTZ,
    recover_until TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX space_library_items_space_idx ON space_library_items(space_id,lifecycle_state,added_at DESC,id);
CREATE INDEX space_library_items_file_idx ON space_library_items(file_id);

CREATE TABLE space_item_aliases (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('library_item','attachment','album','group','person','system_collection','direct_reference')),
    target_id TEXT NOT NULL,
    alias TEXT NOT NULL CHECK (alias ~ '^[a-z0-9][a-z0-9_-]{2,63}$'),
    normalized_alias TEXT NOT NULL CHECK (normalized_alias=lower(alias)),
    created_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(space_id,normalized_alias),
    UNIQUE(space_id,target_kind,target_id)
);

CREATE TABLE space_library_uploads (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    security_domain_id TEXT NOT NULL REFERENCES security_domains(id) ON DELETE RESTRICT,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    object_key TEXT NOT NULL UNIQUE,
    original_filename TEXT NOT NULL CHECK (char_length(original_filename) BETWEEN 1 AND 255),
    purpose TEXT NOT NULL CHECK (purpose IN ('library','attachment')),
    client_declared_mime_type TEXT NOT NULL DEFAULT '',
    requested_byte_size BIGINT NOT NULL CHECK (requested_byte_size > 0),
    client_sha256 TEXT NOT NULL CHECK (client_sha256 ~ '^[0-9a-f]{64}$'),
    verified_byte_size BIGINT,
    verified_sha256 TEXT,
    detected_mime_type TEXT,
    state TEXT NOT NULL CHECK (state IN ('initiated','uploading','uploaded_unverified','quarantined','scanning','processing','ready','rejected','infected','invalid','expired','processing_failed','deleted')),
    file_id TEXT REFERENCES library_files(id) ON DELETE RESTRICT,
    upload_token_hash TEXT NOT NULL,
    error_code TEXT,
    expires_at TIMESTAMPTZ NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finalized_at TIMESTAMPTZ
);
CREATE INDEX space_library_uploads_expiry_idx ON space_library_uploads(state,expires_at);
CREATE INDEX space_library_uploads_user_idx ON space_library_uploads(space_id,user_id,created_at DESC);

CREATE TABLE space_upload_reservations (
    upload_id TEXT PRIMARY KEY REFERENCES space_library_uploads(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    reserved_bytes BIGINT NOT NULL CHECK (reserved_bytes > 0),
    state TEXT NOT NULL CHECK (state IN ('active','consumed','released')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX space_upload_reservations_usage_idx ON space_upload_reservations(space_id,user_id,state);

CREATE TABLE space_storage_contributions (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    file_id TEXT REFERENCES library_files(id) ON DELETE RESTRICT,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('attachment','library_item','import','duplicate','edit','export')),
    source_id TEXT NOT NULL,
    logical_bytes BIGINT NOT NULL CHECK (logical_bytes > 0),
    state TEXT NOT NULL CHECK (state IN ('active','recovery','released')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    released_at TIMESTAMPTZ,
    UNIQUE(space_id,user_id,source_kind,source_id)
);
CREATE INDEX space_storage_contributions_usage_idx ON space_storage_contributions(space_id,user_id,state);

CREATE TABLE space_member_storage_usage (
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    contributed_bytes BIGINT NOT NULL DEFAULT 0 CHECK (contributed_bytes >= 0),
    reserved_bytes BIGINT NOT NULL DEFAULT 0 CHECK (reserved_bytes >= 0),
    version BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(space_id,user_id)
);

CREATE TABLE space_message_attachments (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    message_id TEXT REFERENCES space_messages(id) ON DELETE SET NULL,
    file_id TEXT NOT NULL REFERENCES library_files(id) ON DELETE RESTRICT,
    upload_id TEXT NOT NULL UNIQUE REFERENCES space_library_uploads(id) ON DELETE RESTRICT,
    uploader_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 255),
    promoted_item_id TEXT REFERENCES space_library_items(id) ON DELETE SET NULL,
    lifecycle_state TEXT NOT NULL DEFAULT 'ready' CHECK (lifecycle_state IN ('ready','recovery','purging','deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    recover_until TIMESTAMPTZ
);
CREATE INDEX space_message_attachments_message_idx ON space_message_attachments(message_id);
CREATE INDEX space_message_attachments_space_idx ON space_message_attachments(space_id,created_at DESC);

CREATE TABLE library_item_versions (
    id TEXT PRIMARY KEY,
    space_library_item_id TEXT NOT NULL REFERENCES space_library_items(id) ON DELETE CASCADE,
    parent_version_id TEXT REFERENCES library_item_versions(id) ON DELETE RESTRICT,
    created_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    rendition_blob_id TEXT REFERENCES library_blobs(id) ON DELETE RESTRICT,
    edit_definition JSONB NOT NULL DEFAULT '{}'::jsonb,
    lifecycle_state TEXT NOT NULL DEFAULT 'ready' CHECK (lifecycle_state IN ('ready','recovery','purging','deleted')),
    version_number BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    UNIQUE(space_library_item_id,version_number)
);
ALTER TABLE space_library_items ADD CONSTRAINT space_library_items_current_version_fk
    FOREIGN KEY(current_edit_version_id) REFERENCES library_item_versions(id) ON DELETE SET NULL;

CREATE TABLE library_derivatives (
    id TEXT PRIMARY KEY,
    security_domain_id TEXT NOT NULL REFERENCES security_domains(id) ON DELETE RESTRICT,
    source_file_id TEXT NOT NULL REFERENCES library_files(id) ON DELETE CASCADE,
    space_library_item_id TEXT REFERENCES space_library_items(id) ON DELETE CASCADE,
    derivative_blob_id TEXT REFERENCES library_blobs(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('thumbnail','image_preview','document_preview','video_transcode','audio_waveform','ocr','ai_metadata','embedding','face_embedding','search_document','export','duplicate_fingerprint')),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    lifecycle_state TEXT NOT NULL DEFAULT 'processing' CHECK (lifecycle_state IN ('processing','ready','failed','recovery','purging','deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX library_derivatives_file_idx ON library_derivatives(source_file_id,kind);

CREATE TABLE space_library_audit_events (
    id BIGSERIAL PRIMARY KEY,
    request_id TEXT NOT NULL,
    security_domain_id TEXT REFERENCES security_domains(id) ON DELETE SET NULL,
    space_id TEXT REFERENCES spaces(id) ON DELETE SET NULL,
    actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    actor_kind TEXT NOT NULL DEFAULT 'user' CHECK (actor_kind IN ('user','service','system')),
    action TEXT NOT NULL,
    target_kind TEXT NOT NULL,
    target_id TEXT,
    outcome TEXT NOT NULL CHECK (outcome IN ('success','denied','failed')),
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX space_library_audit_space_idx ON space_library_audit_events(space_id,created_at DESC);

CREATE TABLE space_roles (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    position INTEGER NOT NULL DEFAULT 0,
    is_everyone BOOLEAN NOT NULL DEFAULT FALSE,
    permissions JSONB NOT NULL DEFAULT '[]'::jsonb,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX space_roles_everyone_idx ON space_roles(space_id) WHERE is_everyone;

INSERT INTO space_roles(id,space_id,name,is_everyone,permissions)
SELECT 'role_everyone_'||md5(id),id,'@everyone',TRUE,'["space.view","messages.read","library.view","library.download","storage.view_own_usage"]'::jsonb
FROM spaces;

CREATE TABLE space_member_roles (
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id TEXT NOT NULL REFERENCES space_roles(id) ON DELETE CASCADE,
    assigned_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(space_id,user_id,role_id)
);

CREATE TABLE space_member_permission_overrides (
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    permission TEXT NOT NULL,
    effect TEXT NOT NULL CHECK (effect IN ('allow','deny')),
    updated_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(space_id,user_id,permission)
);

ALTER TABLE space_messages ADD COLUMN reply_to_message_id TEXT REFERENCES space_messages(id) ON DELETE SET NULL;
CREATE INDEX space_messages_reply_idx ON space_messages(reply_to_message_id);

CREATE OR REPLACE FUNCTION misty_can_access_security_domain(candidate_domain_id TEXT)
RETURNS BOOLEAN LANGUAGE SQL STABLE SECURITY DEFINER
SET search_path=public,pg_temp SET row_security=off AS $$
    SELECT EXISTS(
        SELECT 1 FROM security_domains d
        WHERE d.id=candidate_domain_id AND (
            d.owner_user_id=misty_rls_user_id() OR
            (d.space_id IS NOT NULL AND EXISTS(
                SELECT 1 FROM space_members m WHERE m.space_id=d.space_id AND m.user_id=misty_rls_user_id()
            ))
        )
    )
$$;

ALTER TABLE security_domains ENABLE ROW LEVEL SECURITY; ALTER TABLE security_domains FORCE ROW LEVEL SECURITY;
CREATE POLICY security_domains_policy ON security_domains FOR ALL
USING (misty_rls_is_service() OR misty_can_access_security_domain(id))
WITH CHECK (misty_rls_is_service() OR owner_user_id=misty_rls_user_id());

ALTER TABLE library_blobs ENABLE ROW LEVEL SECURITY; ALTER TABLE library_blobs FORCE ROW LEVEL SECURITY;
CREATE POLICY library_blobs_policy ON library_blobs FOR ALL
USING (misty_rls_is_service() OR misty_can_access_security_domain(security_domain_id))
WITH CHECK (misty_rls_is_service() OR misty_can_access_security_domain(security_domain_id));

ALTER TABLE library_files ENABLE ROW LEVEL SECURITY; ALTER TABLE library_files FORCE ROW LEVEL SECURITY;
CREATE POLICY library_files_policy ON library_files FOR ALL
USING (misty_rls_is_service() OR misty_can_access_security_domain(security_domain_id))
WITH CHECK (misty_rls_is_service() OR misty_can_access_security_domain(security_domain_id));

ALTER TABLE space_library_items ENABLE ROW LEVEL SECURITY; ALTER TABLE space_library_items FORCE ROW LEVEL SECURITY;
CREATE POLICY space_library_items_policy ON space_library_items FOR ALL
USING (misty_rls_is_service() OR misty_is_space_member(space_id))
WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

ALTER TABLE space_item_aliases ENABLE ROW LEVEL SECURITY; ALTER TABLE space_item_aliases FORCE ROW LEVEL SECURITY;
CREATE POLICY space_item_aliases_policy ON space_item_aliases FOR ALL
USING (misty_rls_is_service() OR misty_is_space_member(space_id))
WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

ALTER TABLE space_library_uploads ENABLE ROW LEVEL SECURITY; ALTER TABLE space_library_uploads FORCE ROW LEVEL SECURITY;
CREATE POLICY space_library_uploads_policy ON space_library_uploads FOR ALL
USING (misty_rls_is_service() OR (user_id=misty_rls_user_id() AND misty_is_space_member(space_id)))
WITH CHECK (misty_rls_is_service() OR (user_id=misty_rls_user_id() AND misty_is_space_member(space_id)));

ALTER TABLE space_upload_reservations ENABLE ROW LEVEL SECURITY; ALTER TABLE space_upload_reservations FORCE ROW LEVEL SECURITY;
CREATE POLICY space_upload_reservations_policy ON space_upload_reservations FOR ALL
USING (misty_rls_is_service() OR (user_id=misty_rls_user_id() AND misty_is_space_member(space_id)))
WITH CHECK (misty_rls_is_service() OR (user_id=misty_rls_user_id() AND misty_is_space_member(space_id)));

ALTER TABLE space_storage_contributions ENABLE ROW LEVEL SECURITY; ALTER TABLE space_storage_contributions FORCE ROW LEVEL SECURITY;
CREATE POLICY space_storage_contributions_policy ON space_storage_contributions FOR ALL
USING (misty_rls_is_service() OR (user_id=misty_rls_user_id() AND misty_is_space_member(space_id)) OR misty_is_space_owner(space_id))
WITH CHECK (misty_rls_is_service() OR (user_id=misty_rls_user_id() AND misty_is_space_member(space_id)));

ALTER TABLE space_member_storage_usage ENABLE ROW LEVEL SECURITY; ALTER TABLE space_member_storage_usage FORCE ROW LEVEL SECURITY;
CREATE POLICY space_member_storage_usage_policy ON space_member_storage_usage FOR ALL
USING (misty_rls_is_service() OR (user_id=misty_rls_user_id() AND misty_is_space_member(space_id)) OR misty_is_space_owner(space_id))
WITH CHECK (misty_rls_is_service() OR (user_id=misty_rls_user_id() AND misty_is_space_member(space_id)));

ALTER TABLE space_message_attachments ENABLE ROW LEVEL SECURITY; ALTER TABLE space_message_attachments FORCE ROW LEVEL SECURITY;
CREATE POLICY space_message_attachments_policy ON space_message_attachments FOR ALL
USING (misty_rls_is_service() OR misty_is_space_member(space_id))
WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

ALTER TABLE library_item_versions ENABLE ROW LEVEL SECURITY; ALTER TABLE library_item_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY library_item_versions_policy ON library_item_versions FOR ALL
USING (misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_library_items i WHERE i.id=space_library_item_id AND misty_is_space_member(i.space_id)))
WITH CHECK (misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_library_items i WHERE i.id=space_library_item_id AND misty_is_space_member(i.space_id)));

ALTER TABLE library_derivatives ENABLE ROW LEVEL SECURITY; ALTER TABLE library_derivatives FORCE ROW LEVEL SECURITY;
CREATE POLICY library_derivatives_policy ON library_derivatives FOR ALL
USING (misty_rls_is_service() OR misty_can_access_security_domain(security_domain_id))
WITH CHECK (misty_rls_is_service() OR misty_can_access_security_domain(security_domain_id));

ALTER TABLE space_library_audit_events ENABLE ROW LEVEL SECURITY; ALTER TABLE space_library_audit_events FORCE ROW LEVEL SECURITY;
CREATE POLICY space_library_audit_policy ON space_library_audit_events FOR SELECT
USING (misty_rls_is_service() OR (space_id IS NOT NULL AND misty_is_space_owner(space_id)));
CREATE POLICY space_library_audit_insert ON space_library_audit_events FOR INSERT
WITH CHECK (misty_rls_is_service() OR (space_id IS NOT NULL AND misty_is_space_member(space_id)));

ALTER TABLE space_roles ENABLE ROW LEVEL SECURITY; ALTER TABLE space_roles FORCE ROW LEVEL SECURITY;
CREATE POLICY space_roles_read ON space_roles FOR SELECT USING (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_roles_owner_write ON space_roles FOR ALL USING (misty_rls_is_service() OR misty_is_space_owner(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_owner(space_id));

ALTER TABLE space_member_roles ENABLE ROW LEVEL SECURITY; ALTER TABLE space_member_roles FORCE ROW LEVEL SECURITY;
CREATE POLICY space_member_roles_read ON space_member_roles FOR SELECT USING (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_member_roles_owner_write ON space_member_roles FOR ALL USING (misty_rls_is_service() OR misty_is_space_owner(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_owner(space_id));

ALTER TABLE space_member_permission_overrides ENABLE ROW LEVEL SECURITY; ALTER TABLE space_member_permission_overrides FORCE ROW LEVEL SECURITY;
CREATE POLICY space_member_overrides_read ON space_member_permission_overrides FOR SELECT USING (misty_rls_is_service() OR misty_is_space_member(space_id));
CREATE POLICY space_member_overrides_owner_write ON space_member_permission_overrides FOR ALL USING (misty_rls_is_service() OR misty_is_space_owner(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_owner(space_id));

DO $grant$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON security_domains,library_blobs,library_files,space_library_items,space_item_aliases,space_library_uploads,space_upload_reservations,space_storage_contributions,space_member_storage_usage,space_message_attachments,library_item_versions,library_derivatives,space_library_audit_events,space_roles,space_member_roles,space_member_permission_overrides TO misty_app;
        GRANT USAGE,SELECT ON SEQUENCE space_library_audit_events_id_seq TO misty_app;
    END IF;
END $grant$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS misty_can_access_security_domain(TEXT);
ALTER TABLE space_messages DROP COLUMN IF EXISTS reply_to_message_id;
DROP TABLE IF EXISTS space_member_permission_overrides,space_member_roles,space_roles,space_library_audit_events,library_derivatives,library_item_versions,space_message_attachments,space_member_storage_usage,space_storage_contributions,space_upload_reservations,space_library_uploads,space_item_aliases,space_library_items,library_files,library_blobs CASCADE;
ALTER TABLE spaces DROP COLUMN IF EXISTS security_domain_id;
DROP TABLE IF EXISTS security_domains;
DROP INDEX IF EXISTS spaces_owner_idx;
CREATE UNIQUE INDEX spaces_one_additional_per_user_idx ON spaces(owner_user_id) WHERE NOT is_personal;
-- +goose StatementEnd
