-- +goose Up
-- The Home page is gone. Remove its visit and recent-app history, and the
-- Home briefing and AI preferences that only applied on that page.
DROP TABLE user_home_activity;
DROP TABLE user_global_home_activity;
DROP TABLE user_app_activity;
DELETE FROM ai_recaps WHERE surface_id = 'home';
DELETE FROM ai_surface_preferences WHERE surface_id = 'home';

-- +goose Down
CREATE TABLE user_app_activity (
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    app_id text NOT NULL CHECK (char_length(app_id) BETWEEN 1 AND 80),
    open_count bigint NOT NULL DEFAULT 1 CHECK (open_count > 0),
    last_opened_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, app_id)
);
CREATE INDEX user_app_activity_recent_idx ON user_app_activity (user_id, last_opened_at DESC);
ALTER TABLE user_app_activity ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_app_activity FORCE ROW LEVEL SECURITY;
CREATE POLICY user_app_activity_owner_policy ON user_app_activity
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());
GRANT SELECT, INSERT, DELETE, UPDATE ON TABLE user_app_activity TO misty_app;

CREATE TABLE user_global_home_activity (
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    activity_date date NOT NULL,
    visit_count integer NOT NULL DEFAULT 1 CHECK (visit_count BETWEEN 1 AND 1000000),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, activity_date)
);
ALTER TABLE user_global_home_activity ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_global_home_activity FORCE ROW LEVEL SECURITY;
CREATE POLICY user_global_home_activity_owner_policy ON user_global_home_activity
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());
GRANT SELECT, INSERT, DELETE, UPDATE ON TABLE user_global_home_activity TO misty_app;

CREATE TABLE user_home_activity (
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    activity_date date NOT NULL,
    visit_count integer NOT NULL DEFAULT 1 CHECK (visit_count BETWEEN 1 AND 1000000),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, space_id, activity_date)
);
CREATE INDEX user_home_activity_recent_idx ON user_home_activity (user_id, space_id, activity_date DESC);
ALTER TABLE user_home_activity ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_home_activity FORCE ROW LEVEL SECURITY;
CREATE POLICY user_home_activity_owner_policy ON user_home_activity
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());
GRANT SELECT, INSERT, DELETE, UPDATE ON TABLE user_home_activity TO misty_app;
