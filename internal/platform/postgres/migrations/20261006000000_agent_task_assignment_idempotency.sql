-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

-- The HTTP request, realtime delivery, or a worker retry may all attempt to
-- queue the same assignment. Make the assignment version the database-level
-- idempotency boundary so only one durable run can ever be created.
CREATE UNIQUE INDEX space_runs_task_assignment_once_idx
    ON space_runs(source_task_id,agent_id,(action_envelope->>'assignment_task_version'))
    WHERE trigger_kind='task_assignment' AND source_task_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS space_runs_task_assignment_once_idx;
-- +goose StatementEnd
