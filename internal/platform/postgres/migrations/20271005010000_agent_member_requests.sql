-- +goose Up
-- A member publishes one of their agents to a Space so other members' agents
-- can send it work. Unpublished agents cannot be reached.
CREATE TABLE space_agent_listings (
    space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    agent_id text NOT NULL REFERENCES misty_ask_identities(id) ON DELETE CASCADE,
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 500),
    -- ask: the owner approves each request before it runs or is charged.
    -- auto: requests start at once. off: the agent takes no requests.
    accept_policy text NOT NULL DEFAULT 'ask' CHECK (accept_policy IN ('ask', 'auto', 'off')),
    max_open_requests integer NOT NULL DEFAULT 3 CHECK (max_open_requests BETWEEN 1 AND 10),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (space_id, agent_id)
);
CREATE INDEX space_agent_listings_owner ON space_agent_listings(owner_user_id);

-- One agent asking another member's agent for work. The work itself is a
-- delegated run owned by the target agent's owner and billed to the requester.
CREATE TABLE agent_member_requests (
    id text PRIMARY KEY,
    space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    requester_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requester_agent_id text NOT NULL DEFAULT '',
    requester_run_id text NOT NULL CHECK (char_length(requester_run_id) BETWEEN 1 AND 200),
    target_agent_id text NOT NULL REFERENCES misty_ask_identities(id) ON DELETE CASCADE,
    target_owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    child_run_id text NOT NULL REFERENCES space_runs(id) ON DELETE CASCADE,
    message text NOT NULL CHECK (char_length(message) BETWEEN 1 AND 16000),
    idempotency_key text NOT NULL CHECK (char_length(idempotency_key) BETWEEN 1 AND 200),
    -- A listing that asks first waits for its owner. Its run stays queued with
    -- no job until approval, so nothing runs or is charged before then.
    approval text NOT NULL DEFAULT 'not_needed' CHECK (approval IN ('not_needed', 'pending', 'approved', 'declined', 'expired')),
    decided_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (requester_run_id, idempotency_key),
    UNIQUE (child_run_id)
);
CREATE INDEX agent_member_requests_target ON agent_member_requests(target_agent_id, created_at DESC);
CREATE INDEX agent_member_requests_requester ON agent_member_requests(requester_user_id, created_at DESC);
CREATE INDEX agent_member_requests_pending ON agent_member_requests(target_owner_user_id, created_at) WHERE approval = 'pending';

ALTER TABLE space_agent_listings ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_agent_listings FORCE ROW LEVEL SECURITY;
CREATE POLICY space_agent_listings_members ON space_agent_listings
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
ALTER TABLE agent_member_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_member_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_member_requests_parties ON agent_member_requests
    USING (misty_rls_is_service() OR requester_user_id = misty_rls_user_id() OR target_owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service());

-- Both parties' apps refresh when a request changes; owners see listing edits.
CREATE TRIGGER agent_member_requests_target_changes AFTER INSERT OR UPDATE OR DELETE ON agent_member_requests
    FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('agent_requests', 'target_owner_user_id');
CREATE TRIGGER agent_member_requests_requester_changes AFTER INSERT OR UPDATE OR DELETE ON agent_member_requests
    FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('agent_requests', 'requester_user_id');
CREATE TRIGGER space_agent_listings_changes AFTER INSERT OR UPDATE OR DELETE ON space_agent_listings
    FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('agent_listings', 'owner_user_id');

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_agent_listings, agent_member_requests TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS agent_member_requests;
DROP TABLE IF EXISTS space_agent_listings;
