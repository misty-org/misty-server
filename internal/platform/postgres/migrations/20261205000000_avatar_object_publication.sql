-- +goose Up
-- +goose StatementBegin
ALTER TABLE users ADD COLUMN avatar_object_key TEXT
  CHECK (avatar_object_key IS NULL OR avatar_object_key ~ '^avatars/avatar_[0-9a-f-]{36}$');
CREATE UNIQUE INDEX users_avatar_object_key_idx ON users(avatar_object_key) WHERE avatar_object_key IS NOT NULL;
ALTER TABLE object_deletion_jobs ADD COLUMN created_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX object_deletion_jobs_creator_idx ON object_deletion_jobs(created_by_user_id) WHERE created_by_user_id IS NOT NULL;

-- A replaced pointer or deleted account must retain its object cleanup intent,
-- including writes/deletions performed by the compatible Go rollback build.
CREATE FUNCTION queue_replaced_user_avatar() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET app.rls_mode='service' AS $$
DECLARE old_key TEXT; next_key TEXT;
BEGIN
  IF OLD.avatar_version > 0 THEN
    old_key := COALESCE(OLD.avatar_object_key,'avatars/' || OLD.id);
    IF TG_OP = 'UPDATE' AND NEW.avatar_version > 0 THEN
      next_key := COALESCE(NEW.avatar_object_key,'avatars/' || NEW.id);
    END IF;
    IF old_key IS DISTINCT FROM next_key THEN
      INSERT INTO public.object_deletion_jobs(object_key,not_before,created_by_user_id) VALUES(old_key,now()+interval '5 minutes',CASE WHEN TG_OP='DELETE' THEN NULL ELSE OLD.id END)
        ON CONFLICT(object_key) DO UPDATE SET not_before=GREATEST(object_deletion_jobs.not_before,EXCLUDED.not_before);
    END IF;
  END IF;
  IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION queue_replaced_user_avatar() FROM PUBLIC;
CREATE TRIGGER user_avatar_replaced AFTER UPDATE OF avatar_object_key,avatar_version ON users FOR EACH ROW EXECUTE FUNCTION queue_replaced_user_avatar();
CREATE TRIGGER user_avatar_deleted AFTER DELETE ON users FOR EACH ROW EXECUTE FUNCTION queue_replaced_user_avatar();
-- +goose StatementEnd
-- +goose Down
-- Forward only: keep published avatar pointers and their cleanup intents.
SELECT 1;
