-- +goose Up
-- Agents use all of a person's own connected accounts in every Space, so the
-- per-Space picks, their seed record and the pick card kind are gone.
DELETE FROM agent_app_requests WHERE kind = 'pick';
ALTER TABLE agent_app_requests DROP CONSTRAINT IF EXISTS agent_app_requests_kind_check;
ALTER TABLE agent_app_requests ADD CONSTRAINT agent_app_requests_kind_check CHECK (kind IN ('connect', 'approve'));
DROP TABLE IF EXISTS space_integration_seen;
DROP TABLE IF EXISTS space_integration_picks;

-- +goose Down
ALTER TABLE agent_app_requests DROP CONSTRAINT IF EXISTS agent_app_requests_kind_check;
ALTER TABLE agent_app_requests ADD CONSTRAINT agent_app_requests_kind_check CHECK (kind IN ('connect', 'approve', 'pick'));
CREATE TABLE space_integration_picks (
    space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    connected_account_id text NOT NULL CHECK (connected_account_id ~ '^[A-Za-z0-9_-]{1,160}$'),
    toolkit text NOT NULL CHECK (toolkit ~ '^[a-z0-9][a-z0-9_-]{1,63}$'),
    picked_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (space_id, owner_user_id, connected_account_id),
    FOREIGN KEY (space_id, owner_user_id) REFERENCES space_members(space_id, user_id) ON DELETE CASCADE
);
CREATE INDEX space_integration_picks_owner ON space_integration_picks(owner_user_id, connected_account_id);
CREATE TABLE space_integration_seen (
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    connected_account_id text NOT NULL CHECK (connected_account_id ~ '^[A-Za-z0-9_-]{1,160}$'),
    seen_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_user_id, connected_account_id)
);
ALTER TABLE space_integration_picks ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_integration_picks FORCE ROW LEVEL SECURITY;
CREATE POLICY space_integration_picks_owner ON space_integration_picks
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR (owner_user_id = misty_rls_user_id() AND misty_is_space_member(space_id)));
ALTER TABLE space_integration_seen ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_integration_seen FORCE ROW LEVEL SECURITY;
CREATE POLICY space_integration_seen_owner ON space_integration_seen
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_integration_picks, space_integration_seen TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd
