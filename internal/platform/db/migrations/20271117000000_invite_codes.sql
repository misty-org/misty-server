-- +goose Up
-- A person's invite link, /r/<code> on the website, which shows who invited you. The code
-- only names its owner: it grants nothing and nobody records who used it.
CREATE TABLE invite_codes (
  user_id text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  code text NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE invite_codes ENABLE ROW LEVEL SECURITY;
ALTER TABLE invite_codes FORCE ROW LEVEL SECURITY;
CREATE POLICY invite_codes_service ON invite_codes
  USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

-- +goose Down
DROP TABLE IF EXISTS invite_codes;
