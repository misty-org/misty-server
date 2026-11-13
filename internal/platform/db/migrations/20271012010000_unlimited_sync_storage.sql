-- +goose Up
-- Sync is unlimited on every plan: encrypted browser sync writes no longer
-- record cloud byte changes, so they never ask billing for storage.
DROP TRIGGER cloud_bytes ON browser_sync_records;
DROP TRIGGER cloud_bytes ON browser_sync_nodes;
DROP TRIGGER cloud_bytes ON browser_sync_slots;
DROP TRIGGER cloud_bytes ON browser_sync_blobs;
DROP TRIGGER cloud_bytes ON browser_sync_events;
DROP TRIGGER cloud_bytes ON browser_sync_changes;

-- +goose Down
CREATE TRIGGER cloud_bytes BEFORE INSERT OR UPDATE OR DELETE ON browser_sync_records FOR EACH ROW EXECUTE FUNCTION record_account_cloud_change('vault','ciphertext');
CREATE TRIGGER cloud_bytes BEFORE INSERT OR UPDATE OR DELETE ON browser_sync_nodes FOR EACH ROW EXECUTE FUNCTION record_account_cloud_change('vault','ciphertext');
CREATE TRIGGER cloud_bytes BEFORE INSERT OR UPDATE OR DELETE ON browser_sync_slots FOR EACH ROW EXECUTE FUNCTION record_account_cloud_change('vault','ciphertext');
CREATE TRIGGER cloud_bytes BEFORE INSERT OR UPDATE OR DELETE ON browser_sync_blobs FOR EACH ROW EXECUTE FUNCTION record_account_cloud_change('vault','ciphertext');
CREATE TRIGGER cloud_bytes BEFORE INSERT OR UPDATE OR DELETE ON browser_sync_events FOR EACH ROW EXECUTE FUNCTION record_account_cloud_change('vault','envelope');
CREATE TRIGGER cloud_bytes BEFORE INSERT OR UPDATE OR DELETE ON browser_sync_changes FOR EACH ROW EXECUTE FUNCTION record_account_cloud_change('vault','manifest');
