-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE personal_agent_task_run_jobs (
    run_id TEXT PRIMARY KEY REFERENCES space_runs(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    task_id TEXT NOT NULL REFERENCES space_tasks(id) ON DELETE CASCADE,
    agent_id TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'queued'
        CHECK(state IN ('queued','leased','completed','failed','canceled')),
    attempt INTEGER NOT NULL DEFAULT 0 CHECK(attempt BETWEEN 0 AND 3),
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_owner TEXT,
    lease_expires_at TIMESTAMPTZ,
    last_error_code TEXT NOT NULL DEFAULT '',
    last_error_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    CHECK((state='leased')=(lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL))
);

CREATE INDEX personal_agent_task_run_jobs_claim_idx
    ON personal_agent_task_run_jobs(available_at,created_at)
    WHERE state IN ('queued','leased');

ALTER TABLE personal_agent_task_run_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE personal_agent_task_run_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY personal_agent_task_run_jobs_service_policy ON personal_agent_task_run_jobs FOR ALL
    USING(misty_rls_is_service()) WITH CHECK(misty_rls_is_service());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON personal_agent_task_run_jobs TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS personal_agent_task_run_jobs;
-- +goose StatementEnd
