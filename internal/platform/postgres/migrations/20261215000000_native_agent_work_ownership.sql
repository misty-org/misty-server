-- +goose Up
ALTER TABLE space_runs ADD COLUMN execution_owner TEXT NOT NULL DEFAULT 'go'
    CHECK(execution_owner IN ('go','hono'));
ALTER TABLE space_workflow_event_claims ADD COLUMN execution_owner TEXT NOT NULL DEFAULT 'go'
    CHECK(execution_owner IN ('go','hono'));
ALTER TABLE space_workflow_event_claims ADD COLUMN native_request JSONB;
CREATE INDEX native_workflow_claims_pending ON space_workflow_event_claims(created_at,instance_id)
    WHERE execution_owner='hono' AND state='claimed' AND run_id IS NULL;
-- +goose Down
-- Ownership and pending dispatch inputs survive an application rollback.
SELECT 1;
