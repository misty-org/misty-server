-- +goose Up
-- Transaction-local native byte changes. There are deliberately no allowances,
-- rates, prices or plan decisions in this schema. Application commit asks billing.
CREATE TABLE account_cloud_mutations (
 transaction_id bigint NOT NULL,
 user_id text NOT NULL,
 added_bytes bigint NOT NULL DEFAULT 0,
 removed_bytes bigint NOT NULL DEFAULT 0,
 PRIMARY KEY(transaction_id,user_id)
);
-- +goose StatementBegin
CREATE FUNCTION record_account_cloud_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE row_value jsonb; account text; bytes bigint; direction integer; i integer;
BEGIN
 FOR i IN 1..2 LOOP
  IF i=1 THEN
   IF TG_OP='INSERT' THEN CONTINUE; END IF;
   row_value:=to_jsonb(OLD);direction:=-1;
  ELSE
   IF TG_OP='DELETE' THEN CONTINUE; END IF;
   row_value:=to_jsonb(NEW);direction:=1;
  END IF;
  IF TG_ARGV[0]='vault' THEN
   SELECT user_id INTO account FROM browser_sync_vaults WHERE vault_id=(row_value->>'vault_id')::uuid;
  ELSE account:=row_value->>TG_ARGV[0]; END IF;
  IF account IS NULL THEN CONTINUE; END IF;
  IF TG_ARGV[1]='note_payload' THEN
   bytes:=octet_length(row_value->>'title_projection')+octet_length(row_value->>'markdown_projection')+octet_length(row_value->>'plain_text_projection')+octet_length((row_value->'shared_tags')::text);
  ELSIF TG_ARGV[1]='logical_bytes' THEN
   bytes:=CASE WHEN row_value->>'state' IN ('active','recovery') THEN (row_value->>'logical_bytes')::bigint ELSE 0 END;
  ELSIF TG_ARGV[1]='reserved_bytes' THEN
   bytes:=CASE WHEN row_value->>'state'='active' THEN (row_value->>'reserved_bytes')::bigint ELSE 0 END;
  ELSIF TG_ARGV[1] IN ('ciphertext','state') THEN
   bytes:=COALESCE(octet_length((row_value->>TG_ARGV[1])::bytea),0);
  ELSE bytes:=COALESCE(octet_length((row_value->TG_ARGV[1])::text),0); END IF;
  IF bytes=0 THEN CONTINUE; END IF;
  PERFORM pg_advisory_xact_lock(hashtext('storage-personal:'||account));
  INSERT INTO account_cloud_mutations(transaction_id,user_id,added_bytes,removed_bytes)
   VALUES(txid_current(),account,CASE WHEN direction=1 THEN bytes ELSE 0 END,CASE WHEN direction=-1 THEN bytes ELSE 0 END)
   ON CONFLICT(transaction_id,user_id) DO UPDATE
   SET added_bytes=account_cloud_mutations.added_bytes+EXCLUDED.added_bytes,removed_bytes=account_cloud_mutations.removed_bytes+EXCLUDED.removed_bytes;
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END $$;
-- +goose StatementEnd
CREATE TRIGGER cloud_bytes BEFORE INSERT OR UPDATE OR DELETE ON space_storage_contributions FOR EACH ROW EXECUTE FUNCTION record_account_cloud_change('user_id','logical_bytes');
CREATE TRIGGER cloud_bytes BEFORE INSERT OR UPDATE OR DELETE ON space_upload_reservations FOR EACH ROW EXECUTE FUNCTION record_account_cloud_change('user_id','reserved_bytes');
CREATE TRIGGER cloud_bytes BEFORE INSERT OR UPDATE OR DELETE ON space_rendition_reservations FOR EACH ROW EXECUTE FUNCTION record_account_cloud_change('user_id','reserved_bytes');
CREATE TRIGGER cloud_bytes BEFORE INSERT OR UPDATE OR DELETE ON space_messages FOR EACH ROW EXECUTE FUNCTION record_account_cloud_change('sender_user_id','content');
CREATE TRIGGER cloud_bytes BEFORE INSERT OR UPDATE OR DELETE ON space_notes FOR EACH ROW EXECUTE FUNCTION record_account_cloud_change('creator_user_id','note_payload');
CREATE TRIGGER cloud_bytes BEFORE INSERT OR UPDATE OR DELETE ON browser_sync_records FOR EACH ROW EXECUTE FUNCTION record_account_cloud_change('vault','ciphertext');
CREATE TRIGGER cloud_bytes BEFORE INSERT OR UPDATE OR DELETE ON browser_sync_nodes FOR EACH ROW EXECUTE FUNCTION record_account_cloud_change('vault','ciphertext');
CREATE TRIGGER cloud_bytes BEFORE INSERT OR UPDATE OR DELETE ON browser_sync_slots FOR EACH ROW EXECUTE FUNCTION record_account_cloud_change('vault','ciphertext');
CREATE TRIGGER cloud_bytes BEFORE INSERT OR UPDATE OR DELETE ON browser_sync_blobs FOR EACH ROW EXECUTE FUNCTION record_account_cloud_change('vault','ciphertext');
CREATE TRIGGER cloud_bytes BEFORE INSERT OR UPDATE OR DELETE ON browser_sync_events FOR EACH ROW EXECUTE FUNCTION record_account_cloud_change('vault','envelope');
CREATE TRIGGER cloud_bytes BEFORE INSERT OR UPDATE OR DELETE ON browser_sync_changes FOR EACH ROW EXECUTE FUNCTION record_account_cloud_change('vault','manifest');
-- +goose Down
DROP TRIGGER cloud_bytes ON browser_sync_changes;
DROP TRIGGER cloud_bytes ON space_storage_contributions;
DROP TRIGGER cloud_bytes ON space_upload_reservations;
DROP TRIGGER cloud_bytes ON space_rendition_reservations;
DROP TRIGGER cloud_bytes ON space_messages;
DROP TRIGGER cloud_bytes ON space_notes;
DROP TRIGGER cloud_bytes ON browser_sync_records;
DROP TRIGGER cloud_bytes ON browser_sync_nodes;
DROP TRIGGER cloud_bytes ON browser_sync_slots;
DROP TRIGGER cloud_bytes ON browser_sync_blobs;
DROP TRIGGER cloud_bytes ON browser_sync_events;
DROP FUNCTION record_account_cloud_change();
DROP TABLE account_cloud_mutations;
