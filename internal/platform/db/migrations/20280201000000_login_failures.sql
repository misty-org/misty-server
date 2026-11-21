-- +goose Up
-- Failed sign-ins, kept in the database so a restart doesn't forget them and every server
-- instance counts the same ones. A key is a hash of what it counts (an email, an email from
-- one address, a setup key's address), never the email or address itself. Rows older than the
-- lockout window are pruned as new failures come in.
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE login_failures (
  key text NOT NULL CHECK (char_length(key) = 64),
  failed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX login_failures_key_idx ON login_failures (key, failed_at DESC);
CREATE INDEX login_failures_failed_at_idx ON login_failures (failed_at);

ALTER TABLE login_failures ENABLE ROW LEVEL SECURITY;
ALTER TABLE login_failures FORCE ROW LEVEL SECURITY;
CREATE POLICY login_failures_policy ON login_failures
  USING (public.misty_rls_is_service())
  WITH CHECK (public.misty_rls_is_service());

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, DELETE ON login_failures TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);
DROP TABLE IF EXISTS login_failures;
