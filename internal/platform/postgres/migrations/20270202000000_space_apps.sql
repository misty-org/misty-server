-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

-- Detach immutable SDK recovery records before resetting installation authority.
-- Existing targets and invocations still reference these versions.
ALTER TABLE sdk_provider_versions DROP CONSTRAINT sdk_provider_versions_installation_fkey;
ALTER TABLE sdk_app_publishers DROP CONSTRAINT sdk_app_publishers_user_id_app_id_fkey;

-- Beta reset: installations and their dependent grants only. Space content,
-- accounts, memberships and connected credentials are deliberately preserved.
DELETE FROM user_app_installations;
DELETE FROM app_runtime_sessions;
DELETE FROM app_personal_records;
CREATE TABLE space_app_installations (
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    app_id TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'installed' CHECK(state IN ('installed','recoverable')),
    installed_version TEXT NOT NULL,
    permission_version INTEGER NOT NULL CHECK(permission_version > 0),
    granted_scopes JSONB NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(granted_scopes)='array'),
    pin_rank BIGINT NOT NULL,
    authority_generation BIGINT NOT NULL DEFAULT 1,
    release_metadata JSONB NOT NULL DEFAULT '{}',
    installed_by TEXT NOT NULL REFERENCES users(id),
    installed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    uninstalled_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(space_id,app_id)
);
CREATE INDEX space_apps_navigation ON space_app_installations(space_id,state,pin_rank);
ALTER TABLE space_app_installations ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_app_installations FORCE ROW LEVEL SECURITY;
CREATE POLICY space_apps_service ON space_app_installations FOR ALL
    USING(misty_rls_is_service()) WITH CHECK(misty_rls_is_service());
CREATE POLICY space_apps_member_read ON space_app_installations FOR SELECT
    USING(EXISTS(SELECT 1 FROM space_members m WHERE m.space_id=space_app_installations.space_id AND m.user_id=misty_rls_user_id()));

ALTER TABLE app_runtime_sessions DROP CONSTRAINT app_runtime_sessions_user_id_app_id_fkey;
ALTER TABLE app_runtime_sessions ALTER COLUMN space_id SET NOT NULL;
ALTER TABLE app_runtime_sessions ADD FOREIGN KEY(space_id,app_id) REFERENCES space_app_installations(space_id,app_id) ON DELETE CASCADE;
ALTER TABLE app_personal_records DROP CONSTRAINT app_personal_records_user_id_app_id_fkey;
ALTER TABLE app_personal_records ADD COLUMN space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE;
ALTER TABLE app_personal_records DROP CONSTRAINT app_personal_records_pkey;
ALTER TABLE app_personal_records ADD PRIMARY KEY(user_id,space_id,app_id,record_key);
ALTER TABLE app_personal_records ADD FOREIGN KEY(space_id,app_id) REFERENCES space_app_installations(space_id,app_id) ON DELETE CASCADE;

CREATE TABLE personal_space_templates (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK(char_length(name) BETWEEN 1 AND 80),
    description TEXT NOT NULL DEFAULT '' CHECK(char_length(description)<=1000),
    apps JSONB NOT NULL CHECK(jsonb_typeof(apps)='array'),
    version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
ALTER TABLE personal_space_templates ENABLE ROW LEVEL SECURITY;
ALTER TABLE personal_space_templates FORCE ROW LEVEL SECURITY;
CREATE POLICY personal_space_templates_owner ON personal_space_templates FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
  GRANT SELECT,INSERT,UPDATE,DELETE ON space_app_installations,personal_space_templates TO misty_app;
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'The beta installation reset is irreversible; restore a database backup to downgrade'; END $$;
-- +goose StatementEnd
