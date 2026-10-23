-- +goose Up
-- The integrations tree: a person connects each app account once (Composio
-- holds the tokens) and each Space lists which of their accounts it uses.
-- Picks are private to their owner, even in a shared Space.
CREATE TABLE space_integration_picks (
    space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Composio's connected account id; it belongs to the owner's Composio user.
    connected_account_id text NOT NULL CHECK (connected_account_id ~ '^[A-Za-z0-9_-]{1,160}$'),
    toolkit text NOT NULL CHECK (toolkit ~ '^[a-z0-9][a-z0-9_-]{1,63}$'),
    picked_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (space_id, owner_user_id, connected_account_id),
    -- A person's picks leave a Space with them.
    FOREIGN KEY (space_id, owner_user_id) REFERENCES space_members(space_id, user_id) ON DELETE CASCADE
);
CREATE INDEX space_integration_picks_owner ON space_integration_picks(owner_user_id, connected_account_id);

-- Accounts Misty has already seen. A new account is picked once in the
-- personal Space; unpicking it later is never undone. Connected accounts live
-- in Composio, so the seed runs when Misty first lists them, not here.
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

-- A run whose Space has none of the person's accounts for an app asks them to
-- pick one, the way it asks them to connect an app they don't have.
ALTER TABLE agent_app_requests DROP CONSTRAINT IF EXISTS agent_app_requests_kind_check;
ALTER TABLE agent_app_requests ADD CONSTRAINT agent_app_requests_kind_check CHECK (kind IN ('connect', 'approve', 'pick'));
-- Connect and pick cards name the Space whose picks they complete.
ALTER TABLE agent_app_requests ADD COLUMN space_id text NOT NULL DEFAULT '' CHECK (char_length(space_id) <= 200);
DROP INDEX IF EXISTS agent_app_requests_open;
CREATE UNIQUE INDEX agent_app_requests_open ON agent_app_requests(owner_user_id, kind, subject, arguments_hash, space_id)
    WHERE state IN ('pending', 'approved');

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_integration_picks, space_integration_seen TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DELETE FROM agent_app_requests WHERE kind = 'pick';
DROP INDEX IF EXISTS agent_app_requests_open;
DELETE FROM agent_app_requests WHERE state IN ('pending', 'approved') AND space_id <> '';
ALTER TABLE agent_app_requests DROP COLUMN IF EXISTS space_id;
CREATE UNIQUE INDEX agent_app_requests_open ON agent_app_requests(owner_user_id, kind, subject, arguments_hash)
    WHERE state IN ('pending', 'approved');
ALTER TABLE agent_app_requests DROP CONSTRAINT IF EXISTS agent_app_requests_kind_check;
ALTER TABLE agent_app_requests ADD CONSTRAINT agent_app_requests_kind_check CHECK (kind IN ('connect', 'approve'));
DROP TABLE IF EXISTS space_integration_seen;
DROP TABLE IF EXISTS space_integration_picks;
