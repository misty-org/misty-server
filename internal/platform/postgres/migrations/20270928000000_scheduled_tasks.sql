-- +goose Up
-- Personal scheduled tasks: a prompt Misty runs in the cloud on a schedule. Every run is a
-- normal metered AI invocation posted into the task's own conversation.
CREATE TABLE scheduled_tasks (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    conversation_id text REFERENCES misty_ask_conversations(id) ON DELETE SET NULL,
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 120),
    prompt text NOT NULL CHECK (char_length(prompt) BETWEEN 1 AND 8000),
    cadence text NOT NULL CHECK (cadence IN ('once','daily','weekdays','weekly','monthly')),
    local_time text NOT NULL CHECK (local_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
    weekday smallint NOT NULL DEFAULT 1 CHECK (weekday BETWEEN 0 AND 6),
    month_day smallint NOT NULL DEFAULT 1 CHECK (month_day BETWEEN 1 AND 31),
    run_on date,
    timezone text NOT NULL CHECK (char_length(timezone) BETWEEN 1 AND 100),
    enabled boolean NOT NULL DEFAULT true,
    state text NOT NULL DEFAULT 'idle' CHECK (state IN ('idle','running','failed')),
    next_run_at timestamptz,
    lease_until timestamptz,
    last_invocation_id text REFERENCES ai_invocations(id) ON DELETE SET NULL,
    last_run_at timestamptz,
    last_error text NOT NULL DEFAULT '' CHECK (char_length(last_error) <= 2000),
    run_count integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (cadence <> 'once' OR run_on IS NOT NULL)
);
CREATE INDEX scheduled_tasks_due_idx ON scheduled_tasks(next_run_at) WHERE enabled;
CREATE INDEX scheduled_tasks_owner_idx ON scheduled_tasks(user_id, created_at);

ALTER TABLE scheduled_tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE scheduled_tasks FORCE ROW LEVEL SECURITY;
CREATE POLICY scheduled_tasks_owner_policy ON scheduled_tasks
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE scheduled_tasks TO misty_app;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE scheduled_tasks;
