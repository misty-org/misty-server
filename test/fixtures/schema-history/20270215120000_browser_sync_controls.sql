-- +goose Up
ALTER TABLE browser_sync_devices
  ADD COLUMN IF NOT EXISTS display_name TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS platform TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS control_version INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS full_sync BOOLEAN NOT NULL DEFAULT true,
  ADD COLUMN IF NOT EXISTS activation_request UUID,
  ADD COLUMN IF NOT EXISTS activation_expires_at TIMESTAMPTZ;

-- Requests remain identifiable after expiry so a delayed signed takeover can
-- consume its counter without unexpectedly switching the active device.
CREATE TABLE IF NOT EXISTS browser_sync_control_requests (
  workspace_id UUID NOT NULL,
  device_id UUID NOT NULL,
  operation_id UUID NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp() + interval '30 seconds',
  PRIMARY KEY(workspace_id, operation_id),
  FOREIGN KEY(workspace_id,device_id) REFERENCES browser_sync_devices(workspace_id,device_id) ON DELETE CASCADE
);

-- +goose Down
-- Retain device preferences across application rollback.
SELECT 1;
