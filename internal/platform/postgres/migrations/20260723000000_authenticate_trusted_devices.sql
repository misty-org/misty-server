-- +goose Up
-- +goose StatementBegin
CREATE TABLE trusted_device_request_nonces (
    device_id TEXT NOT NULL REFERENCES trusted_devices(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    nonce TEXT NOT NULL CHECK (char_length(nonce) BETWEEN 16 AND 200),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (device_id, nonce)
);
CREATE INDEX trusted_device_request_nonces_expiry_idx ON trusted_device_request_nonces(expires_at);

ALTER TABLE trusted_device_request_nonces ENABLE ROW LEVEL SECURITY;
ALTER TABLE trusted_device_request_nonces FORCE ROW LEVEL SECURITY;
CREATE POLICY trusted_device_request_nonces_user_policy ON trusted_device_request_nonces
    FOR ALL USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, DELETE ON trusted_device_request_nonces TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS trusted_device_request_nonces;
-- +goose StatementEnd
