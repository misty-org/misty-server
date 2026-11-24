-- +goose Up
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE sessions
  ADD COLUMN name text NOT NULL DEFAULT '' CHECK (char_length(name) <= 128),
  ADD COLUMN last_used_at timestamptz;
UPDATE sessions SET last_used_at = GREATEST(created_at, COALESCE(refresh_rotated_at, created_at));
ALTER TABLE sessions
  ALTER COLUMN last_used_at SET DEFAULT now(),
  ALTER COLUMN last_used_at SET NOT NULL;

CREATE TABLE device_sign_in_requests (
  id_hash text PRIMARY KEY CHECK (id_hash ~ '^[0-9a-f]{64}$'),
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
  code_prefix text NOT NULL CHECK (code_prefix ~ '^[BCDFGHJKLMNPQRSTVWXZ]{4}$'),
  code_hash text NOT NULL CHECK (code_hash ~ '^[0-9a-f]{64}$'),
  state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'approved', 'refused', 'expired')),
  failed_attempts integer NOT NULL DEFAULT 0 CHECK (failed_attempts BETWEEN 0 AND 5),
  approved_user_id text REFERENCES users(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  decided_at timestamptz,
  CHECK (expires_at > created_at AND expires_at <= created_at + interval '10 minutes 5 seconds'),
  CHECK ((state = 'approved') = (approved_user_id IS NOT NULL))
);
CREATE UNIQUE INDEX device_sign_in_requests_pending_code_idx
  ON device_sign_in_requests (code_prefix) WHERE state = 'pending';
CREATE INDEX device_sign_in_requests_expires_idx ON device_sign_in_requests (expires_at);
CREATE INDEX device_sign_in_requests_user_idx
  ON device_sign_in_requests (approved_user_id) WHERE approved_user_id IS NOT NULL;
ALTER TABLE device_sign_in_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE device_sign_in_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY device_sign_in_requests_service ON device_sign_in_requests
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON device_sign_in_requests TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);

DROP TABLE IF EXISTS device_sign_in_requests;
ALTER TABLE sessions DROP COLUMN IF EXISTS last_used_at, DROP COLUMN IF EXISTS name;
