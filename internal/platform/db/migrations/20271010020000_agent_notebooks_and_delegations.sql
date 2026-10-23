-- +goose Up
-- A run's notebook (docs/agents/harness.md, Phase 4): its plan and its
-- findings with sources, kept outside the model's context so they survive
-- compaction, shown in the conversation and mirrored to a note.
CREATE TABLE agent_notebooks (
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notebook_key text NOT NULL CHECK (char_length(notebook_key) BETWEEN 1 AND 200),
    note_id text,
    plan text NOT NULL DEFAULT '' CHECK (char_length(plan) <= 20000),
    findings text NOT NULL DEFAULT '' CHECK (char_length(findings) <= 100000),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_user_id, notebook_key)
);
ALTER TABLE agent_notebooks ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_notebooks FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_notebooks_owner ON agent_notebooks
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());

-- Sub-agents a run started with agents_delegate: one row per task, each
-- running as its own invocation under the parent's lease and grants.
CREATE TABLE agent_delegation_children (
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    parent_invocation_id text NOT NULL,
    call_id text NOT NULL CHECK (char_length(call_id) BETWEEN 1 AND 200),
    task_index integer NOT NULL CHECK (task_index BETWEEN 0 AND 15),
    goal text NOT NULL CHECK (char_length(goal) BETWEEN 1 AND 4000),
    output_schema jsonb,
    child_invocation_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (parent_invocation_id, call_id, task_index)
);
CREATE INDEX agent_delegation_children_parent ON agent_delegation_children(owner_user_id, parent_invocation_id);
CREATE UNIQUE INDEX agent_delegation_children_child ON agent_delegation_children(child_invocation_id);
ALTER TABLE agent_delegation_children ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_delegation_children FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_delegation_children_owner ON agent_delegation_children
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON agent_notebooks, agent_delegation_children TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS agent_delegation_children;
DROP TABLE IF EXISTS agent_notebooks;
