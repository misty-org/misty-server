-- +goose Up
-- +goose StatementBegin
ALTER TABLE users
    ADD COLUMN lifecycle_state TEXT NOT NULL DEFAULT 'active'
        CHECK (lifecycle_state IN ('active','pending_deletion','deleted')),
    ADD COLUMN deletion_requested_at TIMESTAMPTZ,
    ADD COLUMN anonymized_at TIMESTAMPTZ;

CREATE TABLE account_deletion_requests (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    status TEXT NOT NULL DEFAULT 'processing'
        CHECK (status IN ('processing','scheduled','completed','failed')),
    status_token_hash TEXT NOT NULL UNIQUE,
    purge_after TIMESTAMPTZ NOT NULL,
    provider_revocation_status JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX account_deletion_requests_active_user_idx
    ON account_deletion_requests(user_id)
    WHERE status IN ('processing','scheduled');
CREATE INDEX account_deletion_requests_due_idx
    ON account_deletion_requests(status,purge_after);

ALTER TABLE account_deletion_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_deletion_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY account_deletion_requests_service
    ON account_deletion_requests FOR ALL
    USING (misty_rls_is_service())
    WITH CHECK (misty_rls_is_service());

DO $grant$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON account_deletion_requests TO misty_app;
    END IF;
END $grant$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS account_deletion_requests;
ALTER TABLE users
    DROP COLUMN IF EXISTS anonymized_at,
    DROP COLUMN IF EXISTS deletion_requested_at,
    DROP COLUMN IF EXISTS lifecycle_state;
-- +goose StatementEnd
