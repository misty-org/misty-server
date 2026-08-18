-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE space_runs
    ADD COLUMN runtime_kind TEXT NOT NULL DEFAULT '',
    ADD COLUMN runtime_run_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN runtime_phase TEXT NOT NULL DEFAULT '',
    ADD COLUMN runtime_heartbeat_at TIMESTAMPTZ;

CREATE INDEX space_runs_personal_agent_runtime_idx
    ON space_runs(agent_id,created_at DESC)
    WHERE trigger_kind='task_assignment';
CREATE UNIQUE INDEX space_runs_runtime_run_id_unique
    ON space_runs(runtime_run_id)
    WHERE runtime_run_id<>'';

ALTER TABLE personal_agent_task_run_jobs DROP CONSTRAINT IF EXISTS personal_agent_task_run_jobs_state_check;
ALTER TABLE personal_agent_task_run_jobs DROP CONSTRAINT IF EXISTS personal_agent_task_run_jobs_check;
ALTER TABLE personal_agent_task_run_jobs
    ADD CONSTRAINT personal_agent_task_run_jobs_state_check
    CHECK(state IN ('queued','leased','dispatched','completed','failed','canceled'));
ALTER TABLE personal_agent_task_run_jobs
    ADD CONSTRAINT personal_agent_task_run_jobs_lease_check
    CHECK((state='leased')=(lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL));

CREATE INDEX personal_agent_task_run_jobs_active_agent_idx
    ON personal_agent_task_run_jobs(agent_id,created_at)
    WHERE state IN ('leased','dispatched');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS personal_agent_task_run_jobs_active_agent_idx;
ALTER TABLE personal_agent_task_run_jobs DROP CONSTRAINT IF EXISTS personal_agent_task_run_jobs_lease_check;
ALTER TABLE personal_agent_task_run_jobs DROP CONSTRAINT IF EXISTS personal_agent_task_run_jobs_state_check;
ALTER TABLE personal_agent_task_run_jobs
    ADD CONSTRAINT personal_agent_task_run_jobs_state_check
    CHECK(state IN ('queued','leased','completed','failed','canceled'));
ALTER TABLE personal_agent_task_run_jobs
    ADD CONSTRAINT personal_agent_task_run_jobs_check
    CHECK((state='leased')=(lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL));
DROP INDEX IF EXISTS space_runs_runtime_run_id_unique;
DROP INDEX IF EXISTS space_runs_personal_agent_runtime_idx;
ALTER TABLE space_runs
    DROP COLUMN IF EXISTS runtime_heartbeat_at,
    DROP COLUMN IF EXISTS runtime_phase,
    DROP COLUMN IF EXISTS runtime_run_id,
    DROP COLUMN IF EXISTS runtime_kind;
-- +goose StatementEnd
