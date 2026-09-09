-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout='5s';
ALTER TABLE space_runs DROP CONSTRAINT space_runs_state_check;
ALTER TABLE space_runs ADD CONSTRAINT space_runs_state_check CHECK(state IN (
 'queued','running','awaiting_approval','awaiting_device','awaiting_intervention',
 'completed','completed_with_errors','failed','canceled','rejected','cooldown','retrying'
));
-- Extend the existing wait journal; keep quick-AI identities and foreign keys.
ALTER TABLE ai_intervention_waits ALTER COLUMN invocation_id DROP NOT NULL;
ALTER TABLE ai_intervention_waits ADD COLUMN space_run_id TEXT REFERENCES space_runs(id) ON DELETE CASCADE;
ALTER TABLE ai_intervention_waits ADD CONSTRAINT intervention_one_run CHECK(num_nonnulls(invocation_id,space_run_id)=1);
ALTER TABLE ai_intervention_waits ADD COLUMN run_id TEXT GENERATED ALWAYS AS (COALESCE(invocation_id,space_run_id)) STORED;
CREATE UNIQUE INDEX agent_intervention_call ON ai_intervention_waits(run_id,call_id);
CREATE UNIQUE INDEX agent_intervention_pending ON ai_intervention_waits(run_id) WHERE state='pending';
-- +goose StatementEnd
-- +goose Down
-- Pinned workers and wait/recovery history remain available during rollback.
SELECT 1;
