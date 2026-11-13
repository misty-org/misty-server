-- +goose Up
-- Face grouping (Library People) is removed, for legal reasons: the server
-- keeps no face templates or other biometric data. Its tables went in
-- 20271023000000_spaces_prune; this removes what was left: face embeddings,
-- face jobs, the job and derivative kinds, and the library-faces worker hint.
DROP TABLE IF EXISTS space_person_observations, space_people CASCADE;

-- Each face embedding's blob was used by that derivative alone. Without a row
-- the object is an orphan, which the Library reconciliation deletes from R2.
CREATE TEMPORARY TABLE face_blobs ON COMMIT DROP AS
  SELECT derivative_blob_id AS id FROM library_derivatives WHERE kind = 'face_embedding';
DELETE FROM library_derivatives WHERE kind = 'face_embedding';
DELETE FROM library_blobs b USING face_blobs f
WHERE b.id = f.id
  AND NOT EXISTS (SELECT 1 FROM library_files x WHERE x.blob_id = b.id)
  AND NOT EXISTS (SELECT 1 FROM library_derivatives x WHERE x.derivative_blob_id = b.id)
  AND NOT EXISTS (SELECT 1 FROM library_item_versions x WHERE x.rendition_blob_id = b.id)
  AND NOT EXISTS (SELECT 1 FROM library_exports x WHERE x.export_blob_id = b.id);
DELETE FROM library_processing_jobs WHERE job_kind = 'faces';

ALTER TABLE library_derivatives DROP CONSTRAINT library_derivatives_kind_check;
ALTER TABLE library_derivatives ADD CONSTRAINT library_derivatives_kind_check CHECK (kind = ANY (ARRAY[
  'thumbnail', 'image_preview', 'document_preview', 'video_transcode', 'audio_waveform', 'ocr',
  'ai_metadata', 'embedding', 'search_document', 'export', 'duplicate_fingerprint']));
ALTER TABLE library_processing_jobs DROP CONSTRAINT library_processing_jobs_job_kind_check;
ALTER TABLE library_processing_jobs ADD CONSTRAINT library_processing_jobs_job_kind_check CHECK (job_kind = ANY (ARRAY[
  'verify', 'scan', 'preview', 'ocr', 'metadata', 'ai', 'media', 'edit', 'duplicates', 'export',
  'retention', 'gc', 'quota_reconcile', 'r2_reconcile', 'import']));

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION misty_notify_library_worker() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE item jsonb;
BEGIN
  IF TG_OP = 'DELETE' THEN item := to_jsonb(OLD); ELSE item := to_jsonb(NEW); END IF;
  IF TG_OP = 'UPDATE' AND
    (OLD.state,OLD.available_at,OLD.lease_expires_at,OLD.job_kind,OLD.target_id) IS NOT DISTINCT FROM
    (NEW.state,NEW.available_at,NEW.lease_expires_at,NEW.job_kind,NEW.target_id) THEN RETURN NULL; END IF;
  IF item->>'job_kind' IN ('ai','edit') THEN
    PERFORM pg_notify('misty_worker_events','library-' || (item->>'job_kind'));
  END IF;
  IF TG_OP = 'UPDATE' AND OLD.job_kind IS DISTINCT FROM NEW.job_kind AND OLD.job_kind IN ('ai','edit') THEN
    PERFORM pg_notify('misty_worker_events','library-' || OLD.job_kind);
  END IF;
  RETURN NULL;
END $$;
-- +goose StatementEnd
DROP TRIGGER library_item_worker_notify ON space_library_items;
CREATE TRIGGER library_item_worker_notify AFTER INSERT OR DELETE OR UPDATE OF lifecycle_state,file_id ON space_library_items
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('library-ai','library-edit');
DROP TRIGGER library_file_worker_notify ON library_files;
CREATE TRIGGER library_file_worker_notify AFTER INSERT OR DELETE OR UPDATE OF lifecycle_state,blob_id ON library_files
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('library-ai','library-edit');
DROP TRIGGER library_blob_worker_notify ON library_blobs;
CREATE TRIGGER library_blob_worker_notify AFTER INSERT OR DELETE OR UPDATE OF lifecycle_state ON library_blobs
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('library-ai','library-edit');

-- +goose Down
-- Face grouping is not restored; its data was removed in Up.
SELECT 1;
