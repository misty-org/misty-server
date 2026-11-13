-- +goose Up
-- Tasks by +misty email and @misty Slack are gone: runs start from schedules and
-- from mentions inside Misty. A task still under way could never send its answer
-- back to its thread, so it stops here. Its conversation stays with the person.
SELECT set_config('app.rls_mode', 'service', true);
UPDATE ai_invocations
SET state = 'canceled', error_code = 'retired_feature', canceled_at = NOW(), updated_at = NOW()
WHERE state IN ('queued', 'running', 'awaiting_device', 'awaiting_intervention', 'awaiting_timer')
  AND id IN (SELECT invocation_id FROM agent_inbound_tasks WHERE invocation_id IS NOT NULL);
SELECT set_config('app.rls_mode', '', true);

DROP TABLE agent_inbound_tasks;
DROP TABLE agent_inbound_channels;

-- +goose Down
CREATE TABLE agent_inbound_channels (
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel text NOT NULL CHECK (channel IN ('gmail', 'slack')),
    enabled boolean NOT NULL DEFAULT TRUE,
    connected_account_id text NOT NULL CHECK (char_length(connected_account_id) BETWEEN 1 AND 160),
    identity jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(identity) = 'object'),
    watch_after timestamptz NOT NULL DEFAULT now(),
    next_check_at timestamptz NOT NULL DEFAULT now(),
    last_checked_at timestamptz,
    last_error text NOT NULL DEFAULT '' CHECK (char_length(last_error) <= 500),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, channel)
);
CREATE INDEX agent_inbound_channels_due ON agent_inbound_channels(next_check_at) WHERE enabled;

CREATE TABLE agent_inbound_tasks (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel text NOT NULL CHECK (channel IN ('gmail', 'slack')),
    external_id text NOT NULL CHECK (char_length(external_id) BETWEEN 1 AND 300),
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

CREATE TRIGGER agent_inbound_channels_changes AFTER INSERT OR DELETE OR UPDATE OF enabled, identity, last_error ON agent_inbound_channels
    FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('inbound_channels', 'user_id');

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON agent_inbound_channels, agent_inbound_tasks TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd
