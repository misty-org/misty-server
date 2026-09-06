-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE user_app_installations (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    app_id TEXT NOT NULL CHECK(char_length(app_id) BETWEEN 1 AND 80),
    state TEXT NOT NULL DEFAULT 'installed'
        CHECK(state IN ('installed','recoverable','purging','purged')),
    installed_version TEXT NOT NULL CHECK(char_length(installed_version) BETWEEN 1 AND 40),
    permission_version INTEGER NOT NULL DEFAULT 1 CHECK(permission_version > 0),
    granted_scopes JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(granted_scopes)='array'),
    pinned BOOLEAN NOT NULL DEFAULT TRUE,
    pin_rank BIGINT NOT NULL DEFAULT 0,
    installed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    uninstalled_at TIMESTAMPTZ,
    data_deletion_at TIMESTAMPTZ,
    purged_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(user_id, app_id),
    CHECK(
        (state='installed' AND uninstalled_at IS NULL AND data_deletion_at IS NULL AND purged_at IS NULL)
        OR (state IN ('recoverable','purging') AND uninstalled_at IS NOT NULL AND data_deletion_at IS NOT NULL AND purged_at IS NULL)
        OR (state='purged' AND uninstalled_at IS NOT NULL AND data_deletion_at IS NOT NULL AND purged_at IS NOT NULL)
    )
);
CREATE INDEX user_app_installations_navigation_idx
    ON user_app_installations(user_id, pinned DESC, pin_rank, app_id)
    WHERE state='installed';
CREATE INDEX user_app_installations_deletion_idx
    ON user_app_installations(data_deletion_at, user_id, app_id)
    WHERE state IN ('recoverable','purging');

CREATE TABLE onboarding_completions (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    request_fingerprint TEXT NOT NULL CHECK(char_length(request_fingerprint)=64),
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE RESTRICT,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE app_data_deletion_jobs (
    user_id TEXT NOT NULL,
    app_id TEXT NOT NULL,
    delete_at TIMESTAMPTZ NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending'
        CHECK(state IN ('pending','running','failed','completed','canceled')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts >= 0),
    last_error TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(user_id, app_id),
    FOREIGN KEY(user_id, app_id) REFERENCES user_app_installations(user_id, app_id) ON DELETE CASCADE
);
CREATE INDEX app_data_deletion_jobs_due_idx
    ON app_data_deletion_jobs(delete_at, user_id, app_id)
    WHERE state IN ('pending','failed');

-- Hosted apps receive an opaque, short-lived credential instead of the user's
-- full Misty session. The server resolves the hash to this exact app, optional
-- Space, and immutable scope envelope on every SDK request.
CREATE TABLE app_runtime_sessions (
    token_hash TEXT PRIMARY KEY CHECK(char_length(token_hash)=64),
    user_id TEXT NOT NULL,
    app_id TEXT NOT NULL,
    space_id TEXT REFERENCES spaces(id) ON DELETE CASCADE,
    scopes JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(scopes)='array'),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY(user_id, app_id) REFERENCES user_app_installations(user_id, app_id) ON DELETE CASCADE
);
CREATE INDEX app_runtime_sessions_expiry_idx ON app_runtime_sessions(expires_at);
CREATE INDEX app_runtime_sessions_owner_idx ON app_runtime_sessions(user_id, app_id, expires_at DESC);

-- Private app records are account-owned and app-namespaced. Collaborative
-- content remains in the existing Space tables and is intentionally outside
-- the uninstall purge boundary.
CREATE TABLE app_personal_records (
    user_id TEXT NOT NULL,
    app_id TEXT NOT NULL,
    record_key TEXT NOT NULL CHECK(char_length(record_key) BETWEEN 1 AND 160),
    data JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(user_id, app_id, record_key),
    FOREIGN KEY(user_id, app_id) REFERENCES user_app_installations(user_id, app_id) ON DELETE CASCADE,
    CHECK(jsonb_typeof(data) IN ('object','array','string','number','boolean','null'))
);

CREATE TABLE app_install_events (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    app_id TEXT NOT NULL CHECK(char_length(app_id) BETWEEN 1 AND 80),
    event_type TEXT NOT NULL
        CHECK(event_type IN ('installed','updated','restored','pinned','unpinned','uninstalled','purge_started','purged','purge_failed','permissions_changed')),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(metadata)='object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX app_install_events_user_idx
    ON app_install_events(user_id, created_at DESC, id DESC);

ALTER TABLE user_app_installations ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_app_installations FORCE ROW LEVEL SECURITY;
CREATE POLICY user_app_installations_owner_policy ON user_app_installations FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());

ALTER TABLE app_data_deletion_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_data_deletion_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY app_data_deletion_jobs_owner_policy ON app_data_deletion_jobs FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());

ALTER TABLE app_install_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_install_events FORCE ROW LEVEL SECURITY;
CREATE POLICY app_install_events_owner_policy ON app_install_events FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());

ALTER TABLE app_runtime_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_runtime_sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY app_runtime_sessions_owner_policy ON app_runtime_sessions FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());

ALTER TABLE app_personal_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_personal_records FORCE ROW LEVEL SECURITY;
CREATE POLICY app_personal_records_owner_policy ON app_personal_records FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());

ALTER TABLE onboarding_completions ENABLE ROW LEVEL SECURITY;
ALTER TABLE onboarding_completions FORCE ROW LEVEL SECURITY;
CREATE POLICY onboarding_completions_owner_policy ON onboarding_completions FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON user_app_installations,app_data_deletion_jobs,app_install_events,onboarding_completions,app_runtime_sessions,app_personal_records TO misty_app;
        GRANT USAGE,SELECT ON SEQUENCE app_install_events_id_seq TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app_install_events;
DROP TABLE IF EXISTS app_personal_records;
DROP TABLE IF EXISTS app_runtime_sessions;
DROP TABLE IF EXISTS app_data_deletion_jobs;
DROP TABLE IF EXISTS onboarding_completions;
DROP TABLE IF EXISTS user_app_installations;
-- +goose StatementEnd
