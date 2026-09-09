-- +goose Up
-- +goose StatementBegin
ALTER TABLE ai_invocations DROP CONSTRAINT ai_invocations_state_check;
ALTER TABLE ai_invocations ADD CONSTRAINT ai_invocations_state_check CHECK(state IN ('queued','running','awaiting_approval','awaiting_device','awaiting_intervention','completed','failed','canceled'));
CREATE TABLE ai_intervention_waits (
 id TEXT PRIMARY KEY,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 invocation_id TEXT NOT NULL REFERENCES ai_invocations(id) ON DELETE CASCADE,
 runtime_run_id TEXT NOT NULL,
 call_id TEXT NOT NULL,
 arguments_hash TEXT NOT NULL,
 hook_token TEXT NOT NULL,
 context_id TEXT NOT NULL,
 device_id TEXT NOT NULL,
 scope_id TEXT NOT NULL,
 target_label TEXT NOT NULL,
 action TEXT NOT NULL CHECK(action IN ('sign_in','account_confirmation','challenge','open_target','review')),
 reason TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','ready','declined','expired')),
 expires_at TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 consumed_at TIMESTAMPTZ,
 UNIQUE(invocation_id,call_id)
);
CREATE INDEX ai_intervention_waits_due ON ai_intervention_waits(expires_at) WHERE state='pending';
ALTER TABLE ai_intervention_waits ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_intervention_waits FORCE ROW LEVEL SECURITY;
CREATE POLICY ai_intervention_waits_service ON ai_intervention_waits FOR ALL USING(misty_rls_is_service()) WITH CHECK(misty_rls_is_service());
ALTER TABLE agent_runtime_deliveries DROP CONSTRAINT agent_runtime_deliveries_operation_check;
ALTER TABLE agent_runtime_deliveries ADD CONSTRAINT agent_runtime_deliveries_operation_check CHECK(operation IN ('invocation.start','runtime.cancel','runtime.reconcile','approval.resume','device.resume','intervention.resume'));
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
 GRANT SELECT,INSERT,UPDATE,DELETE ON ai_intervention_waits TO misty_app;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
-- Preserve pending user actions and recovery evidence through rollback.
SELECT 1;
