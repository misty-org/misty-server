-- +goose Up
-- Existing work keeps its Go owner until explicitly drained/migrated.
ALTER TABLE ai_invocations ADD COLUMN runtime_owner TEXT NOT NULL DEFAULT 'go'
    CHECK (runtime_owner IN ('go','hono'));
CREATE TABLE ai_runtime_callback_receipts (
    invocation_id TEXT NOT NULL REFERENCES ai_invocations(id) ON DELETE CASCADE,
    effect_key TEXT NOT NULL,
    body_sha256 TEXT NOT NULL CHECK (body_sha256 ~ '^[a-f0-9]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(invocation_id,effect_key)
);
ALTER TABLE ai_runtime_callback_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_runtime_callback_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY ai_runtime_callback_receipts_service ON ai_runtime_callback_receipts FOR ALL
    USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON ai_runtime_callback_receipts TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
-- Preserve owner and replay evidence during application rollback.
SELECT 1;
