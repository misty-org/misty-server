-- +goose Up
-- +goose StatementBegin
CREATE TABLE password_recovery_jobs (
  id UUID PRIMARY KEY,
  request_order BIGINT GENERATED ALWAYS AS IDENTITY UNIQUE,
  email TEXT NOT NULL,
  token_key_id TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','processing','sent','superseded','expired')),
  issued_user_id TEXT REFERENCES users(id) ON DELETE CASCADE,
  issued_at TIMESTAMPTZ,
  expires_at TIMESTAMPTZ NOT NULL,
  available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  lease_owner UUID,
  lease_expires_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX password_recovery_jobs_pending ON password_recovery_jobs(available_at,created_at,id) WHERE state='pending';
CREATE INDEX password_recovery_jobs_leases ON password_recovery_jobs(lease_expires_at) WHERE state='processing';
CREATE INDEX password_recovery_jobs_email ON password_recovery_jobs(email,created_at DESC,id DESC);
CREATE INDEX password_recovery_jobs_expiry ON password_recovery_jobs(expires_at);
ALTER TABLE password_recovery_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE password_recovery_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY password_recovery_jobs_service ON password_recovery_jobs FOR ALL USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
-- +goose StatementEnd

-- +goose Down
DROP TABLE password_recovery_jobs;
