-- +goose Up
CREATE TABLE space_invitation_delivery_jobs (
    invite_id TEXT PRIMARY KEY REFERENCES space_invitations(id) ON DELETE CASCADE,
    generation UUID NOT NULL,
    token_key_id TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','processing','sent','superseded')),
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts>=0),
    lease_id UUID,
    lease_expires_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX space_invitation_delivery_jobs_due_idx ON space_invitation_delivery_jobs(available_at,invite_id) WHERE state='pending';
CREATE INDEX space_invitation_delivery_jobs_lease_idx ON space_invitation_delivery_jobs(lease_expires_at) WHERE state='processing';
ALTER TABLE space_invitation_delivery_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_invitation_delivery_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY space_invitation_delivery_jobs_service ON space_invitation_delivery_jobs FOR ALL
    USING(misty_rls_is_service()) WITH CHECK(misty_rls_is_service());
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_invitation_delivery_jobs TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
DROP TABLE space_invitation_delivery_jobs;
