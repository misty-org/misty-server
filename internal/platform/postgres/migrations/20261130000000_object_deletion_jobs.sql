-- +goose Up
CREATE TABLE object_deletion_jobs (
    object_key TEXT PRIMARY KEY,
    not_before TIMESTAMPTZ NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    lease_id UUID,
    lease_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX object_deletion_jobs_due_idx ON object_deletion_jobs(not_before,lease_expires_at);
ALTER TABLE object_deletion_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE object_deletion_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY object_deletion_jobs_service ON object_deletion_jobs FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON object_deletion_jobs TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
DROP TABLE object_deletion_jobs;
