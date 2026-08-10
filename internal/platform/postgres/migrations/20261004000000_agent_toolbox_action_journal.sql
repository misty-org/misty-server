-- +goose Up
-- +goose StatementBegin
-- Durable, surface-independent audit and idempotency journal for every
-- server-executed Agent Toolbox write. Workflow actions keep their more
-- detailed node journal as well; this table is the common product audit seam.

CREATE TABLE agent_toolbox_action_journal (
    idempotency_key TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    space_id TEXT,
    agent_id TEXT,
    agent_instance_id TEXT,
    run_id TEXT,
    session_id TEXT,
    tool_name TEXT NOT NULL,
    audit_event TEXT NOT NULL,
    risk TEXT NOT NULL CHECK(risk IN ('write','dangerous')),
    source TEXT NOT NULL,
    request JSONB NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(request)='object'),
    result JSONB NOT NULL DEFAULT '{}'::jsonb,
    state TEXT NOT NULL CHECK(state IN ('started','completed','failed')),
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX agent_toolbox_action_journal_user_created_idx
    ON agent_toolbox_action_journal(user_id,created_at DESC);
CREATE INDEX agent_toolbox_action_journal_run_idx
    ON agent_toolbox_action_journal(run_id,created_at)
    WHERE run_id IS NOT NULL;
CREATE INDEX agent_toolbox_action_journal_instance_idx
    ON agent_toolbox_action_journal(agent_instance_id,created_at DESC)
    WHERE agent_instance_id IS NOT NULL;

ALTER TABLE agent_toolbox_action_journal ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_toolbox_action_journal FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_toolbox_action_journal_private ON agent_toolbox_action_journal
    FOR ALL
    USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
    WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON agent_toolbox_action_journal TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS agent_toolbox_action_journal;
