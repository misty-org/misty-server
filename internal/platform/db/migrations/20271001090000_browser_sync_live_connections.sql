-- +goose Up
-- Sync liveness without per-device writes. Each API process holds one lease;
-- a connection row exists only while its socket is open on a live process.
-- Older processes keep using per-connection expires_at during a rolling update.
CREATE TABLE browser_sync_instances(
  instance_id uuid PRIMARY KEY,
  expires_at timestamptz NOT NULL
);
ALTER TABLE browser_sync_connections ADD COLUMN instance_id uuid;
CREATE INDEX browser_sync_connections_instance_idx ON browser_sync_connections(instance_id) WHERE instance_id IS NOT NULL;
CREATE INDEX browser_sync_connections_device_idx ON browser_sync_connections(vault_id,device_id);
-- Last-seen survives deletion of the device's closed connection rows.
ALTER TABLE browser_sync_devices ADD COLUMN last_seen_at timestamptz;
UPDATE browser_sync_devices d SET last_seen_at=c.seen
FROM (SELECT vault_id,device_id,max(last_seen_at) AS seen FROM browser_sync_connections GROUP BY 1,2) c
WHERE c.vault_id=d.vault_id AND c.device_id=d.device_id;
-- The account session that minted a ticket; open sockets revalidate it in band.
ALTER TABLE browser_sync_tickets ADD COLUMN session_hash text;

-- +goose Down
ALTER TABLE browser_sync_tickets DROP COLUMN IF EXISTS session_hash;
ALTER TABLE browser_sync_devices DROP COLUMN IF EXISTS last_seen_at;
DROP INDEX IF EXISTS browser_sync_connections_device_idx;
DROP INDEX IF EXISTS browser_sync_connections_instance_idx;
-- Rows written by new processes look closed to older code once their lease is gone.
UPDATE browser_sync_connections SET expires_at=clock_timestamp() WHERE instance_id IS NOT NULL;
ALTER TABLE browser_sync_connections DROP COLUMN IF EXISTS instance_id;
DROP TABLE IF EXISTS browser_sync_instances;
