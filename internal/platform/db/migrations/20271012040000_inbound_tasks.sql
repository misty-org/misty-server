-- +goose Up
-- Give the agent a task by email or Slack (roadmap C8). The person turns a
-- channel on; Misty watches their own connected Gmail or Slack through
-- Composio for messages they wrote to it, starts a task for each, and answers
-- in the same thread. Only the person's own messages start tasks.
CREATE TABLE agent_inbound_channels (
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel text NOT NULL CHECK (channel IN ('gmail', 'slack')),
    enabled boolean NOT NULL DEFAULT TRUE,
    -- The Composio connected account, and who it is: the Gmail address (plus
    -- the address to forward to) or the Slack user.
    connected_account_id text NOT NULL CHECK (char_length(connected_account_id) BETWEEN 1 AND 160),
    identity jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(identity) = 'object'),
    -- Messages from before the channel was turned on never start tasks.
    watch_after timestamptz NOT NULL DEFAULT now(),
    next_check_at timestamptz NOT NULL DEFAULT now(),
    last_checked_at timestamptz,
    last_error text NOT NULL DEFAULT '' CHECK (char_length(last_error) <= 500),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, channel)
);
CREATE INDEX agent_inbound_channels_due ON agent_inbound_channels(next_check_at) WHERE enabled;

-- One task per message, so a message never starts two.
CREATE TABLE agent_inbound_tasks (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel text NOT NULL CHECK (channel IN ('gmail', 'slack')),
    external_id text NOT NULL CHECK (char_length(external_id) BETWEEN 1 AND 300),
    -- Where the answer goes: the Gmail thread and message, or the Slack channel and thread.
    thread_ref jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(thread_ref) = 'object'),
    invocation_id text,
    conversation_id text,
    state text NOT NULL DEFAULT 'started' CHECK (state IN ('started', 'replied', 'reply_failed', 'not_started')),
    error text NOT NULL DEFAULT '' CHECK (char_length(error) <= 500),
    created_at timestamptz NOT NULL DEFAULT now(),
    replied_at timestamptz,
    UNIQUE (user_id, channel, external_id)
);
CREATE INDEX agent_inbound_tasks_invocation ON agent_inbound_tasks(invocation_id) WHERE invocation_id IS NOT NULL;

ALTER TABLE agent_inbound_channels ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_inbound_channels FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_inbound_channels_owner ON agent_inbound_channels
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());
ALTER TABLE agent_inbound_tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_inbound_tasks FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_inbound_tasks_owner ON agent_inbound_tasks
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service());

-- Not on every check, only on changes the person sees.
CREATE TRIGGER agent_inbound_channels_changes AFTER INSERT OR DELETE OR UPDATE OF enabled, identity, last_error ON agent_inbound_channels
    FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('inbound_channels', 'user_id');

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON agent_inbound_channels, agent_inbound_tasks TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS agent_inbound_tasks;
DROP TABLE IF EXISTS agent_inbound_channels;
