-- +goose Up
-- A member's agent asks to be told when a task it sent through the A2A
-- endpoint changes. Notifications never leave Misty: they are delivered on the
-- member's own A2A inbox stream, so there is no outbound webhook to abuse.
CREATE TABLE a2a_push_configs (
    request_id text NOT NULL REFERENCES agent_member_requests(id) ON DELETE CASCADE,
    id text NOT NULL CHECK (char_length(id) BETWEEN 1 AND 100),
    requester_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requester_agent_id text NOT NULL REFERENCES misty_ask_identities(id) ON DELETE CASCADE,
    -- Echoed back with each notification so the client can match it.
    token text NOT NULL DEFAULT '' CHECK (char_length(token) <= 512),
    -- The last A2A state handed to an inbox; a change is delivered once.
    delivered_state text NOT NULL DEFAULT '',
    delivered_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (request_id, id)
);
CREATE INDEX a2a_push_configs_inbox ON a2a_push_configs(requester_user_id, requester_agent_id, created_at)
    WHERE delivered_state NOT IN ('completed', 'failed', 'canceled', 'rejected');

ALTER TABLE a2a_push_configs ENABLE ROW LEVEL SECURITY;
ALTER TABLE a2a_push_configs FORCE ROW LEVEL SECURITY;
CREATE POLICY a2a_push_configs_requester ON a2a_push_configs
    USING (misty_rls_is_service() OR requester_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR requester_user_id = misty_rls_user_id());

-- A delegated run changing state changes its request's A2A state. The
-- requester's streams, push inbox and Activity follow this hint instead of
-- polling; the target owner already hears about their own runs.
-- +goose StatementBegin
CREATE FUNCTION misty_notify_member_request_run() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
    AS $$
DECLARE request_id text; requester text;
BEGIN
  SELECT id, requester_user_id INTO request_id, requester FROM agent_member_requests WHERE child_run_id = NEW.id;
  IF FOUND THEN
    PERFORM pg_notify('misty_account_events', json_build_object('userId', requester, 'topic', 'agent_requests', 'id', request_id)::text);
  END IF;
  RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER space_runs_member_request_notify AFTER UPDATE OF state ON space_runs
    FOR EACH ROW WHEN (OLD.state IS DISTINCT FROM NEW.state AND NEW.trigger_kind = 'delegated')
    EXECUTE FUNCTION misty_notify_member_request_run();

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON a2a_push_configs TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS space_runs_member_request_notify ON space_runs;
DROP FUNCTION IF EXISTS misty_notify_member_request_run();
DROP TABLE IF EXISTS a2a_push_configs;
