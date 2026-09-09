-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE TABLE agent_runtime_deliveries (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    run_id TEXT NOT NULL,
    operation TEXT NOT NULL CHECK(operation IN ('invocation.start','runtime.cancel','approval.resume','device.resume')),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','leased','completed','failed')),
    attempts INTEGER NOT NULL DEFAULT 0,
    lease_id TEXT,
    lease_expires_at TIMESTAMPTZ,
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX agent_runtime_deliveries_due ON agent_runtime_deliveries(available_at) WHERE state IN ('pending','leased');
ALTER TABLE agent_runtime_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_runtime_deliveries FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_runtime_deliveries_service ON agent_runtime_deliveries FOR ALL
    USING(misty_rls_is_service()) WITH CHECK(misty_rls_is_service());
ALTER TABLE ai_invocation_events ADD COLUMN receipt_key TEXT;
CREATE UNIQUE INDEX ai_invocation_events_receipt ON ai_invocation_events(invocation_id,receipt_key) WHERE receipt_key IS NOT NULL;
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON agent_runtime_deliveries TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Preserve queued delivery and receipt evidence across application rollback.
SELECT 1;
