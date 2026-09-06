-- +goose Up
ALTER TABLE space_calendar_sources
    ADD COLUMN execution_owner TEXT NOT NULL DEFAULT 'go' CHECK(execution_owner IN ('go','hono')),
    ADD COLUMN native_sync_requested BIGINT NOT NULL DEFAULT 0 CHECK(native_sync_requested>=0),
    ADD COLUMN native_sync_completed BIGINT NOT NULL DEFAULT 0 CHECK(native_sync_completed>=0),
    ADD COLUMN native_sync_available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN native_sync_lease_id UUID,
    ADD COLUMN native_sync_lease_until TIMESTAMPTZ;
CREATE INDEX native_calendar_work_due ON space_calendar_sources(native_sync_available_at,id)
    WHERE execution_owner='hono' AND disabled_at IS NULL AND status<>'disabled';
-- +goose Down
-- Preserve ownership, callback requests and in-flight leases across application rollback.
SELECT 1;
