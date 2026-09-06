-- +goose Up
-- +goose StatementBegin
-- Existing Go records are terminal on insertion. Native writers commit a
-- content-free intent first; a crash leaves an explicitly unfinished record.
ALTER TABLE mail_action_audit ADD COLUMN completed_at TIMESTAMPTZ DEFAULT now();
UPDATE mail_action_audit SET completed_at=created_at;
ALTER TABLE mail_action_audit ADD CONSTRAINT mail_action_completion_valid
    CHECK (completed_at IS NOT NULL OR (NOT success AND error_code='mail_operation_pending'));
CREATE INDEX mail_action_audit_unfinished_idx ON mail_action_audit(created_at,id) WHERE completed_at IS NULL;
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT UPDATE(success,error_code,target_id,completed_at) ON mail_action_audit TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: audit intent and outcome history must survive a rollback.
SELECT 1;
