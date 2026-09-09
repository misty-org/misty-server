-- +goose Up
ALTER TABLE ai_invocations DROP CONSTRAINT ai_invocations_state_check;
ALTER TABLE ai_invocations ADD CONSTRAINT ai_invocations_state_check CHECK(state IN ('queued','running','awaiting_approval','awaiting_device','completed','failed','canceled'));
ALTER TABLE ai_invocations
 ADD COLUMN device_wait_hook_token TEXT NOT NULL DEFAULT '',
 ADD COLUMN device_wait_expires_at TIMESTAMPTZ,
 ADD COLUMN device_wait_context_id TEXT NOT NULL DEFAULT '',
 ADD COLUMN device_wait_scope_id TEXT NOT NULL DEFAULT '',
 ADD COLUMN device_wait_capability TEXT NOT NULL DEFAULT '',
 ADD COLUMN device_wait_call_id TEXT NOT NULL DEFAULT '',
 ADD COLUMN device_wait_arguments_hash TEXT NOT NULL DEFAULT '';
CREATE INDEX ai_invocation_device_waits_due ON ai_invocations(device_wait_expires_at) WHERE state='awaiting_device';
ALTER TABLE workflow_device_node_jobs ADD COLUMN recovery_attempts INTEGER NOT NULL DEFAULT 0;
-- +goose Down
-- Retain wait identities and resumable records when rolling back applications.
SELECT 1;
