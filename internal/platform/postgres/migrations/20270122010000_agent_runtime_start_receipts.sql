-- +goose Up
-- +goose StatementBegin
CREATE TABLE agent_runtime_start_receipts (
    run_id TEXT PRIMARY KEY,
    claim_token TEXT NOT NULL,
    runtime_run_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
ALTER TABLE agent_runtime_start_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_runtime_start_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_runtime_start_receipts_service ON agent_runtime_start_receipts FOR ALL
    USING(misty_rls_is_service()) WITH CHECK(misty_rls_is_service());
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON agent_runtime_start_receipts TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
-- Preserve start uncertainty and runtime identities during rollback.
SELECT 1;
