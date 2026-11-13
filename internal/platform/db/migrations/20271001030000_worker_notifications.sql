-- +goose Up
-- Notifications are committed hints. Durable rows and their deadlines remain
-- authoritative, including after listener loss or an API process restart.
-- +goose StatementBegin
CREATE FUNCTION misty_notify_worker_queue() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE queue text;
BEGIN
  FOREACH queue IN ARRAY TG_ARGV LOOP
    PERFORM pg_notify('misty_worker_events', queue);
  END LOOP;
  RETURN NULL;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION misty_notify_library_worker() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE item jsonb;
BEGIN
  IF TG_OP = 'DELETE' THEN item := to_jsonb(OLD); ELSE item := to_jsonb(NEW); END IF;
  IF TG_OP = 'UPDATE' AND
    (OLD.state,OLD.available_at,OLD.lease_expires_at,OLD.job_kind,OLD.target_id) IS NOT DISTINCT FROM
    (NEW.state,NEW.available_at,NEW.lease_expires_at,NEW.job_kind,NEW.target_id) THEN RETURN NULL; END IF;
  IF item->>'job_kind' IN ('ai','edit','faces') THEN
    PERFORM pg_notify('misty_worker_events','library-' || (item->>'job_kind'));
  END IF;
  IF TG_OP = 'UPDATE' AND OLD.job_kind IS DISTINCT FROM NEW.job_kind AND OLD.job_kind IN ('ai','edit','faces') THEN
    PERFORM pg_notify('misty_worker_events','library-' || OLD.job_kind);
  END IF;
  RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER library_worker_notify AFTER INSERT OR UPDATE OR DELETE ON library_processing_jobs
FOR EACH ROW EXECUTE FUNCTION misty_notify_library_worker();
CREATE TRIGGER library_item_worker_notify AFTER INSERT OR DELETE OR UPDATE OF lifecycle_state,file_id ON space_library_items
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('library-ai','library-edit','library-faces');
CREATE TRIGGER library_file_worker_notify AFTER INSERT OR DELETE OR UPDATE OF lifecycle_state,blob_id ON library_files
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('library-ai','library-edit','library-faces');
CREATE TRIGGER library_blob_worker_notify AFTER INSERT OR DELETE OR UPDATE OF lifecycle_state ON library_blobs
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('library-ai','library-edit','library-faces');
CREATE TRIGGER library_edit_worker_notify AFTER INSERT OR DELETE OR UPDATE OF lifecycle_state ON library_item_versions
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('library-edit');
CREATE TRIGGER library_reservation_worker_notify AFTER INSERT OR DELETE OR UPDATE OF state ON space_rendition_reservations
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('library-edit');
CREATE TRIGGER note_control_worker_notify AFTER INSERT OR DELETE OR UPDATE OF next_attempt_at,delivered_at ON space_note_control_outbox
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('note-control');
CREATE TRIGGER drawing_control_worker_notify AFTER INSERT OR DELETE OR UPDATE OF next_attempt_at,delivered_at ON space_drawing_control_outbox
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('drawing-control','drawing-purge');
CREATE TRIGGER drawing_purge_worker_notify AFTER INSERT OR DELETE OR UPDATE OF lifecycle_state ON space_drawings
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('drawing-purge');
CREATE TRIGGER embedding_chunk_worker_notify AFTER INSERT OR DELETE OR UPDATE OF embedding,embedding_lease_until ON ai_retrieval_chunks
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('embedding');
CREATE TRIGGER embedding_document_worker_notify AFTER INSERT OR DELETE OR UPDATE OF lifecycle_state,owner_user_id ON ai_retrieval_documents
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('embedding');

CREATE TRIGGER social_command_worker_notify AFTER INSERT OR DELETE OR UPDATE OF state,available_at,lease_expires_at,binding_id ON social_outbound_commands
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('social');
CREATE TRIGGER social_schedule_worker_notify AFTER INSERT OR DELETE OR UPDATE OF status,scheduled_at,authority_id ON social_scheduled_messages
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('social');
CREATE TRIGGER social_authority_worker_notify AFTER INSERT OR DELETE OR UPDATE OF allow_scheduled,revoked_at ON social_send_authorities
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('social');
CREATE TRIGGER social_binding_worker_notify AFTER INSERT OR DELETE OR UPDATE OF status,disabled_at ON social_bindings
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('social');
CREATE TRIGGER billing_outbox_worker_notify AFTER INSERT OR DELETE OR UPDATE OF delivered_at,available_at ON billing_adapter_outbox
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('billing');
CREATE TRIGGER billing_intent_worker_notify AFTER INSERT OR DELETE OR UPDATE OF state,expires_at ON billing_adapter_intents
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('billing');
CREATE TRIGGER billing_reservation_worker_notify AFTER INSERT OR DELETE ON billing_adapter_reservations
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('billing');
CREATE TRIGGER voice_usage_worker_notify AFTER INSERT OR DELETE OR UPDATE OF state,updated_at ON voice_usage_journal
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('billing');

CREATE INDEX library_worker_available_idx ON library_processing_jobs(job_kind,available_at) WHERE state='queued';
CREATE INDEX library_worker_expiry_idx ON library_processing_jobs(job_kind,lease_expires_at) WHERE state IN ('leased','running');

-- Support the earliest eligible outbound deadline without aggregating the entire queue.
CREATE INDEX social_worker_deadline_idx ON social_outbound_commands((GREATEST(available_at,lease_expires_at))) WHERE state='queued';

-- +goose Down
DROP INDEX IF EXISTS social_worker_deadline_idx;
DROP TRIGGER IF EXISTS voice_usage_worker_notify ON voice_usage_journal;
DROP TRIGGER IF EXISTS billing_reservation_worker_notify ON billing_adapter_reservations;
DROP TRIGGER IF EXISTS billing_intent_worker_notify ON billing_adapter_intents;
DROP TRIGGER IF EXISTS billing_outbox_worker_notify ON billing_adapter_outbox;
DROP TRIGGER IF EXISTS social_binding_worker_notify ON social_bindings;
DROP TRIGGER IF EXISTS social_authority_worker_notify ON social_send_authorities;
DROP TRIGGER IF EXISTS social_schedule_worker_notify ON social_scheduled_messages;
DROP TRIGGER IF EXISTS social_command_worker_notify ON social_outbound_commands;
DROP INDEX IF EXISTS library_worker_expiry_idx;
DROP INDEX IF EXISTS library_worker_available_idx;
DROP TRIGGER IF EXISTS embedding_document_worker_notify ON ai_retrieval_documents;
DROP TRIGGER IF EXISTS embedding_chunk_worker_notify ON ai_retrieval_chunks;
DROP TRIGGER IF EXISTS drawing_purge_worker_notify ON space_drawings;
DROP TRIGGER IF EXISTS drawing_control_worker_notify ON space_drawing_control_outbox;
DROP TRIGGER IF EXISTS note_control_worker_notify ON space_note_control_outbox;
DROP TRIGGER IF EXISTS library_reservation_worker_notify ON space_rendition_reservations;
DROP TRIGGER IF EXISTS library_edit_worker_notify ON library_item_versions;
DROP TRIGGER IF EXISTS library_blob_worker_notify ON library_blobs;
DROP TRIGGER IF EXISTS library_file_worker_notify ON library_files;
DROP TRIGGER IF EXISTS library_item_worker_notify ON space_library_items;
DROP TRIGGER IF EXISTS library_worker_notify ON library_processing_jobs;
DROP FUNCTION IF EXISTS misty_notify_library_worker();
DROP FUNCTION IF EXISTS misty_notify_worker_queue();
