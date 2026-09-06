-- +goose Up
-- +goose StatementBegin
-- Native one-use authorizations are separate from legacy Go states so an old
-- callback handler cannot bypass their session/configuration/credential fences.
CREATE TABLE connection_authorization_requests (
    state_hash TEXT PRIMARY KEY CHECK (state_hash ~ '^[0-9a-f]{64}$'),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('google','microsoft','dropbox','figma','discord','instagram')),
    actor JSONB NOT NULL CHECK (jsonb_typeof(actor)='object'),
    credential_snapshot JSONB NOT NULL CHECK (jsonb_typeof(credential_snapshot)='array'),
    capabilities JSONB NOT NULL CHECK (jsonb_typeof(capabilities)='array'),
    requested_scopes JSONB NOT NULL CHECK (jsonb_typeof(requested_scopes)='array'),
    verifier_ciphertext BYTEA NOT NULL,
    verifier_nonce BYTEA NOT NULL,
    redirect_uri TEXT NOT NULL,
    client_id_hash TEXT NOT NULL CHECK (client_id_hash ~ '^[0-9a-f]{64}$'),
    return_to TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX connection_authorization_requests_expiry_idx ON connection_authorization_requests(expires_at);
CREATE INDEX connection_authorization_requests_owner_idx ON connection_authorization_requests(user_id,created_at DESC);
ALTER TABLE connection_authorization_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE connection_authorization_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY connection_authorization_requests_owner ON connection_authorization_requests FOR ALL
  USING (misty_rls_is_service() OR user_id=misty_rls_user_id())
  WITH CHECK (misty_rls_is_service() OR user_id=misty_rls_user_id());
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
    GRANT SELECT,INSERT,UPDATE,DELETE ON connection_authorization_requests TO misty_app;
  END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
-- Forward only: pending authorizations must not silently lose their fences.
SELECT 1;
