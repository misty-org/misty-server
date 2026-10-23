-- +goose Up
ALTER TABLE users ADD COLUMN provider text NOT NULL DEFAULT 'misty';
ALTER TABLE users ADD COLUMN provider_subject text;
ALTER TABLE users ADD CONSTRAINT users_provider_check CHECK (provider IN ('misty','google'));
ALTER TABLE users ADD CONSTRAINT users_provider_identity_check CHECK (
 (provider='misty' AND provider_subject IS NULL) OR
 (provider='google' AND password_hash='' AND
  ((provider_subject IS NOT NULL AND length(provider_subject)>0) OR lifecycle_state='deleted'))
);
CREATE UNIQUE INDEX users_provider_subject_unique_idx ON users(provider,provider_subject) WHERE provider_subject IS NOT NULL;

CREATE TABLE google_sign_in_flows (
 state_hash text PRIMARY KEY,
 poll_hash text NOT NULL UNIQUE,
 nonce text NOT NULL,
 verifier text NOT NULL,
 phase text NOT NULL DEFAULT 'pending' CHECK (phase IN ('pending','started','exchanging','ready')),
 user_id text REFERENCES users(id) ON DELETE CASCADE,
 reauthenticate_user_id text REFERENCES users(id) ON DELETE CASCADE,
 error_code text NOT NULL DEFAULT '',
 expires_at timestamptz NOT NULL
);
CREATE INDEX google_sign_in_flows_expiry_idx ON google_sign_in_flows(expires_at);
ALTER TABLE google_sign_in_flows ENABLE ROW LEVEL SECURITY;
ALTER TABLE google_sign_in_flows FORCE ROW LEVEL SECURITY;
CREATE POLICY google_sign_in_flows_service ON google_sign_in_flows
 USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

CREATE TABLE google_reauthentication_tokens (
 token_hash text PRIMARY KEY,
 user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 expires_at timestamptz NOT NULL
);
ALTER TABLE google_reauthentication_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE google_reauthentication_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY google_reauthentication_service ON google_reauthentication_tokens
 USING (public.misty_rls_is_service()) WITH CHECK (public.misty_rls_is_service());

-- +goose Down
-- Provider identity is intentionally retained on rollback: dropping it would
-- silently turn Google identities into password accounts.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Google account identities are forward-only; restore a verified backup to roll back'; END $$;
-- +goose StatementEnd
