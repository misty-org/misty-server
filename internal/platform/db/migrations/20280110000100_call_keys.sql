-- +goose Up
-- Each computer's public key for calls. A caller seals the call's end-to-end key for every
-- computer of every person in the DM with these; only those computers hold the private keys, so the
-- server (and LiveKit) never can read the call. key_id is the computer's own random id.
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE call_keys (
  user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key_id text NOT NULL CHECK (char_length(key_id) BETWEEN 8 AND 64),
  public_key text NOT NULL CHECK (char_length(public_key) BETWEEN 32 AND 256),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, key_id)
);

ALTER TABLE call_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE call_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY call_keys_policy ON call_keys
  USING (public.misty_rls_is_service())
  WITH CHECK (public.misty_rls_is_service());

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON call_keys TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);
DROP TABLE IF EXISTS call_keys;
