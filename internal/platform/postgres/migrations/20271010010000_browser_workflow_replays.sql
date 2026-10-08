-- +goose Up
-- The browser actions of a workflow's last successful run, with each control
-- named by a locator (role, name, index) rather than a ref, so the next run of
-- that workflow can replay them without model calls and fall back to the agent
-- at the first step the page no longer matches (docs/plans/agentic-browser.md).
CREATE TABLE browser_workflow_replays (
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    method_version_id text NOT NULL CHECK (char_length(method_version_id) BETWEEN 1 AND 200),
    start_url text NOT NULL DEFAULT '' CHECK (char_length(start_url) <= 4096),
    calls jsonb NOT NULL CHECK (jsonb_typeof(calls) = 'array'),
    source_invocation_id text NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_user_id, method_version_id)
);
ALTER TABLE browser_workflow_replays ENABLE ROW LEVEL SECURITY;
ALTER TABLE browser_workflow_replays FORCE ROW LEVEL SECURITY;
CREATE POLICY browser_workflow_replays_owner ON browser_workflow_replays
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON browser_workflow_replays TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS browser_workflow_replays;
