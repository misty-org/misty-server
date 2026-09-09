-- +goose Up
ALTER TABLE ai_invocations ADD COLUMN runtime_observed_at TIMESTAMPTZ, ADD COLUMN runtime_observed_status TEXT NOT NULL DEFAULT '';
CREATE INDEX ai_invocation_runtime_observation_due ON ai_invocations(runtime_observed_at,runtime_heartbeat_at) WHERE runtime_run_id<>'' AND state IN ('running','awaiting_approval','awaiting_device');
-- +goose Down
-- Retain runtime observations through application rollback.
SELECT 1;
