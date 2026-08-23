-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';

-- Interactive Misty invocations now execute in the same durable Vercel
-- WorkflowAgent runtime as creator-owned Agents. These columns are control-plane
-- correlation only; model prompts and credentials never live here.
ALTER TABLE ai_invocations
    ADD COLUMN runtime_kind TEXT NOT NULL DEFAULT '',
    ADD COLUMN runtime_run_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN agent_run_id TEXT REFERENCES space_runs(id) ON DELETE SET NULL,
    ADD COLUMN runtime_heartbeat_at TIMESTAMPTZ;

CREATE UNIQUE INDEX ai_invocations_runtime_run_idx
    ON ai_invocations(runtime_run_id) WHERE runtime_run_id<>'';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS ai_invocations_runtime_run_idx;
ALTER TABLE ai_invocations
    DROP COLUMN IF EXISTS runtime_heartbeat_at,
    DROP COLUMN IF EXISTS agent_run_id,
    DROP COLUMN IF EXISTS runtime_run_id,
    DROP COLUMN IF EXISTS runtime_kind;
-- +goose StatementEnd
