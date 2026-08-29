-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE user_home_activity (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    activity_date DATE NOT NULL,
    visit_count INTEGER NOT NULL DEFAULT 1 CHECK(visit_count BETWEEN 1 AND 1000000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(user_id, space_id, activity_date)
);
CREATE INDEX user_home_activity_recent_idx
    ON user_home_activity(user_id, space_id, activity_date DESC);

CREATE TABLE user_app_activity (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    app_id TEXT NOT NULL CHECK(char_length(app_id) BETWEEN 1 AND 80),
    open_count BIGINT NOT NULL DEFAULT 1 CHECK(open_count > 0),
    last_opened_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(user_id, app_id)
);
CREATE INDEX user_app_activity_recent_idx
    ON user_app_activity(user_id, last_opened_at DESC);

ALTER TABLE user_home_activity ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_home_activity FORCE ROW LEVEL SECURITY;
CREATE POLICY user_home_activity_owner_policy ON user_home_activity FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());

ALTER TABLE user_app_activity ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_app_activity FORCE ROW LEVEL SECURITY;
CREATE POLICY user_app_activity_owner_policy ON user_app_activity FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON user_home_activity,user_app_activity TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS user_app_activity;
DROP TABLE IF EXISTS user_home_activity;
