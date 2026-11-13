-- +goose Up
-- The budget a delegated coding run may spend before asking for more. Each
-- lease reserves from the person's weekly allowance; the device reports what
-- the tool used against it. The token is stored hashed and shown once.
CREATE TABLE coding_leases (
    id text PRIMARY KEY CHECK (id ~ '^lease_[0-9a-f-]{36}$'),
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    invocation_id text NOT NULL,
    device_id text NOT NULL,
    engine text NOT NULL,
    model text NOT NULL DEFAULT '' CHECK (length(model) <= 200),
    token_hash text NOT NULL CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    token_budget bigint NOT NULL CHECK (token_budget > 0),
    used_tokens bigint NOT NULL DEFAULT 0 CHECK (used_tokens >= 0),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX coding_leases_user_week ON coding_leases(user_id, created_at DESC);
CREATE INDEX coding_leases_invocation ON coding_leases(invocation_id);
ALTER TABLE coding_leases ENABLE ROW LEVEL SECURITY;
ALTER TABLE coding_leases FORCE ROW LEVEL SECURITY;
CREATE POLICY coding_leases_owner ON coding_leases
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON coding_leases TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS coding_leases;
