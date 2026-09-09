-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout='5s';
SELECT set_config('app.rls_mode','service',true);

-- Compatibility identity selects exactly one existing authoritative run table.
-- This extends scope metadata; it introduces no new execution state or job queue.
ALTER TABLE agent_sdk_capability_bindings DROP CONSTRAINT agent_sdk_capability_bindings_run_id_fkey;
ALTER TABLE agent_sdk_capability_bindings
 ADD COLUMN space_run_id TEXT GENERATED ALWAYS AS
  (CASE WHEN left(run_id,11)='invocation_' THEN NULL ELSE run_id END) STORED REFERENCES space_runs(id) ON DELETE CASCADE,
 ADD COLUMN ai_invocation_id TEXT GENERATED ALWAYS AS
  (CASE WHEN left(run_id,11)='invocation_' THEN run_id ELSE NULL END) STORED REFERENCES ai_invocations(id) ON DELETE CASCADE;
CREATE INDEX agent_sdk_bindings_space_run_idx ON agent_sdk_capability_bindings(space_run_id) WHERE space_run_id IS NOT NULL;
CREATE INDEX agent_sdk_bindings_ai_invocation_idx ON agent_sdk_capability_bindings(ai_invocation_id) WHERE ai_invocation_id IS NOT NULL;
-- +goose StatementEnd
-- +goose Down
-- Retain pinned versions and active runs during rollback.
SELECT 1;
