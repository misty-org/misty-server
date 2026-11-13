-- +goose Up
-- Connected apps belong to the account. Agents inherit them; there are no
-- per-agent bindings and no calendar-only connection records.
DROP TABLE IF EXISTS composio_agent_bindings;
DROP TABLE IF EXISTS composio_connections;

-- One Composio session per account, reused across runs.
CREATE TABLE composio_sessions (
    owner_user_id text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    session_id text NOT NULL CHECK (length(session_id) BETWEEN 4 AND 160),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- What an agent needs from the user to continue: connect an app, or approve
-- one exact app action. Approvals are single use.
CREATE TABLE agent_app_requests (
    id text PRIMARY KEY,
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    run_id text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('connect', 'approve')),
    subject text NOT NULL CHECK (length(subject) BETWEEN 2 AND 128),
    arguments_hash text NOT NULL DEFAULT '',
    title text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    summary text NOT NULL DEFAULT '' CHECK (length(summary) <= 4000),
    state text NOT NULL CHECK (state IN ('pending', 'connected', 'approved', 'used', 'declined', 'expired')),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    decided_at timestamptz
);
CREATE UNIQUE INDEX agent_app_requests_open ON agent_app_requests(owner_user_id, kind, subject, arguments_hash)
    WHERE state IN ('pending', 'approved');
CREATE INDEX agent_app_requests_recent ON agent_app_requests(owner_user_id, kind, subject, arguments_hash, created_at DESC);

ALTER TABLE composio_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE composio_sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY composio_sessions_owner ON composio_sessions
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
ALTER TABLE agent_app_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_app_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_app_requests_owner ON agent_app_requests
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());

-- One account setting controls autonomy in connected apps.
ALTER TABLE ai_user_settings ADD COLUMN IF NOT EXISTS app_actions_ask boolean NOT NULL DEFAULT true;

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON composio_sessions, agent_app_requests TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE ai_user_settings DROP COLUMN IF EXISTS app_actions_ask;
DROP TABLE IF EXISTS agent_app_requests;
DROP TABLE IF EXISTS composio_sessions;
