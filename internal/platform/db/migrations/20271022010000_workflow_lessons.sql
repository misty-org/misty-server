-- +goose Up
-- Workflows as memory (docs/agents/harness.md, phase 7). A saved workflow has two
-- owners: the person's request (agent_method_versions, which the agent changes only when
-- asked) and the steps the agent learned from running it, kept here as versions. Each
-- successful run may add a version; the newest is offered the next time the workflow runs.
CREATE TABLE agent_method_lessons (
    method_id text NOT NULL,
    user_id text NOT NULL,
    version integer NOT NULL CHECK (version > 0),
    steps text NOT NULL CHECK (char_length(steps) BETWEEN 1 AND 4000),
    source_invocation_id text REFERENCES ai_invocations(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (method_id, user_id, version),
    FOREIGN KEY (method_id, user_id) REFERENCES agent_methods(id, user_id) ON DELETE CASCADE
);
ALTER TABLE agent_method_lessons ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_method_lessons FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_method_lessons_owner ON agent_method_lessons
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, DELETE ON agent_method_lessons TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- Saved workflows' tools take their memory.workflow names; no aliases are kept.
UPDATE agent_toolbox_action_journal SET tool_name = 'memory.workflow.' || substr(tool_name, 8)
    WHERE tool_name IN ('method.list', 'method.save', 'method.use');

-- +goose Down
UPDATE agent_toolbox_action_journal SET tool_name = 'method.' || substr(tool_name, 17)
    WHERE tool_name IN ('memory.workflow.list', 'memory.workflow.save', 'memory.workflow.use');
DROP TABLE agent_method_lessons;
