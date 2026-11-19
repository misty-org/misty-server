-- +goose Up
-- Web hand-off codes: the app, signed in, asks for one to open the website signed in as the
-- same account (to change a plan, say). Each is single-use and lasts two minutes; only its
-- hash is kept.
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE web_handoff_codes (
  token_hash text PRIMARY KEY,
  user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at timestamptz NOT NULL
);
CREATE INDEX web_handoff_codes_expires_idx ON web_handoff_codes(expires_at);
ALTER TABLE web_handoff_codes ENABLE ROW LEVEL SECURITY;
ALTER TABLE web_handoff_codes FORCE ROW LEVEL SECURITY;
CREATE POLICY web_handoff_codes_service ON web_handoff_codes
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);

DROP TABLE IF EXISTS web_handoff_codes;
