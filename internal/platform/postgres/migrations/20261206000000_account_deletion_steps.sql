-- +goose Up
-- +goose StatementBegin
-- Native cleanup is durable and resumable. Existing Go requests are deliberately
-- not adopted by this migration: ownership handover requires an explicit import.
ALTER TABLE account_deletion_requests ADD COLUMN cleanup_owner TEXT NOT NULL DEFAULT 'go' CHECK (cleanup_owner IN ('go','native'));
CREATE TABLE account_deletion_steps (
    request_id TEXT NOT NULL REFERENCES account_deletion_requests(id) ON DELETE CASCADE,
    step TEXT NOT NULL CHECK (step IN ('payments','providers','local','purge')),
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','processing','completed')),
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_token UUID,
    lease_expires_at TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts>=0),
    last_error_code TEXT NOT NULL DEFAULT '',
    result JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(result)='object'),
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(request_id,step),
    CHECK ((state='processing' AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
        OR (state<>'processing' AND lease_token IS NULL AND lease_expires_at IS NULL)),
    CHECK ((state='completed')=(completed_at IS NOT NULL))
);
CREATE INDEX account_deletion_steps_pending_idx ON account_deletion_steps(available_at,request_id,step) WHERE state='pending';
CREATE INDEX account_deletion_steps_processing_idx ON account_deletion_steps(lease_expires_at) WHERE state='processing';
ALTER TABLE account_deletion_steps ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_deletion_steps FORCE ROW LEVEL SECURITY;
CREATE POLICY account_deletion_steps_service ON account_deletion_steps FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON account_deletion_steps TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
-- Keep pending cleanup and its completion evidence during application rollback.
SELECT 1;
