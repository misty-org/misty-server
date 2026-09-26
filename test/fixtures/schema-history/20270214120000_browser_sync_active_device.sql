-- +goose Up
ALTER TABLE browser_sync_workspaces
    ADD COLUMN active_device_id UUID,
    ADD COLUMN active_epoch UUID,
    ADD COLUMN active_seen_at TIMESTAMPTZ,
    ADD CONSTRAINT browser_sync_active_pair CHECK ((active_device_id IS NULL) = (active_epoch IS NULL));
ALTER TABLE browser_sync_receipts ADD COLUMN discarded BOOLEAN NOT NULL DEFAULT false;
COMMENT ON COLUMN browser_sync_workspaces.active_epoch IS 'Signed takeover operation ID. Periodic heartbeats refresh liveness but never claim control.';

-- +goose Down
-- Retain active-device selection and deduplication on application rollback.
SELECT 1;
