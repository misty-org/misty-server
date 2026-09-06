-- +goose Up
-- +goose StatementBegin
CREATE TABLE account_deletion_provider_resources (
    request_id TEXT NOT NULL REFERENCES account_deletion_requests(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('connected','cloud','integration','figma_webhook','provider_subscription')),
    resource_id TEXT NOT NULL,
    provider TEXT NOT NULL,
    credential_kind TEXT CHECK (credential_kind IN ('connected','cloud','integration')),
    credential_id TEXT,
    credential_format TEXT CHECK (credential_format IN ('connected','legacy')),
    ciphertext BYTEA,
    nonce BYTEA,
    key_version SMALLINT,
    source_fingerprint TEXT NOT NULL CHECK (source_fingerprint ~ '^[0-9a-f]{64}$'),
    details JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details)='object'),
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','completed')),
    outcome TEXT NOT NULL DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts>=0),
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error_code TEXT NOT NULL DEFAULT '',
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(request_id,kind,resource_id),
    CHECK ((state='completed')=(completed_at IS NOT NULL)),
    CHECK ((credential_kind IS NULL)=(credential_id IS NULL)),
    CHECK (state<>'completed' OR (ciphertext IS NULL AND nonce IS NULL)),
    CHECK (octet_length(ciphertext)<=2097168),
    CHECK (ciphertext IS NULL OR (nonce IS NOT NULL AND key_version IS NOT NULL AND credential_format IS NOT NULL))
);
CREATE INDEX account_deletion_provider_resources_due_idx ON account_deletion_provider_resources(request_id,available_at,kind,resource_id) WHERE state='pending';
ALTER TABLE account_deletion_provider_resources ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_deletion_provider_resources FORCE ROW LEVEL SECURITY;
CREATE POLICY account_deletion_provider_resources_service ON account_deletion_provider_resources FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON account_deletion_provider_resources TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
-- Preserve retry credentials and acknowledgement evidence during rollback.
SELECT 1;
