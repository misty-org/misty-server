-- +goose Up
-- +goose StatementBegin
-- Cascades must retain both image keys before attachment metadata disappears.
-- Thirty minutes exceeds the existing AI upload URL lifetime of fifteen minutes.
CREATE FUNCTION queue_removed_ai_attachment_objects() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET app.rls_mode='service' AS $$
DECLARE old_key TEXT; retained_keys TEXT[] := ARRAY[]::TEXT[]; owner_id TEXT;
BEGIN
  IF TG_OP='UPDATE' AND NEW.lifecycle_state<>'deleted' THEN
    retained_keys := ARRAY[NEW.object_key,NEW.model_object_key];
  END IF;
  SELECT id INTO owner_id FROM public.users WHERE id=OLD.user_id;
  FOR old_key IN SELECT DISTINCT unnest(ARRAY[OLD.object_key,OLD.model_object_key]) LOOP
    IF NOT(old_key=ANY(retained_keys)) THEN
      INSERT INTO public.object_deletion_jobs(object_key,not_before,created_by_user_id)
        VALUES(old_key,clock_timestamp()+interval '30 minutes',owner_id)
        ON CONFLICT(object_key) DO UPDATE SET not_before=GREATEST(object_deletion_jobs.not_before,EXCLUDED.not_before);
    END IF;
  END LOOP;
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION queue_removed_ai_attachment_objects() FROM PUBLIC;
CREATE TRIGGER ai_attachment_removed_objects AFTER DELETE ON ai_conversation_attachments
  FOR EACH ROW EXECUTE FUNCTION queue_removed_ai_attachment_objects();
CREATE TRIGGER ai_attachment_replaced_objects AFTER UPDATE OF object_key,model_object_key,lifecycle_state ON ai_conversation_attachments
  FOR EACH ROW EXECUTE FUNCTION queue_removed_ai_attachment_objects();
-- +goose StatementEnd
-- +goose Down
-- Forward only: preserve durable cleanup during application rollback.
SELECT 1;
