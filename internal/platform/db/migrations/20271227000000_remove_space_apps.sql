-- +goose Up
-- Misty no longer has Space apps of its own: Planner (tasks), Calendar (events), Whiteboards
-- (drawings), Slides (decks), Data (workbooks) and the Media editors (a Library item's edit
-- versions and their renditions) go, with their rooms' outboxes, their images and every upload
-- bound to them. Every app is now a third party's, so the per-app storage the SDK's store.* kept
-- goes too, and an installed app may carry the pinned build it came from (source). Space notes,
-- which Lab notebooks are, stay. There are no real users yet, so nothing is migrated: the rows go.
SELECT set_config('app.rls_mode', 'service', true);

-- The Library: edit versions and renditions.
DELETE FROM library_processing_jobs WHERE job_kind = 'edit';
DELETE FROM library_recovery_tombstones WHERE target_kind = 'edit';
DELETE FROM library_legal_holds WHERE target_kind = 'edit';
-- Reservations still held for a rendition give their bytes back first.
UPDATE space_storage_usage u SET reserved_bytes = GREATEST(0, u.reserved_bytes - r.bytes), version = u.version + 1, updated_at = now()
FROM (SELECT space_id, sum(reserved_bytes) bytes FROM space_rendition_reservations
      WHERE source_kind = 'edit' AND state = 'active' GROUP BY space_id) r
WHERE u.space_id = r.space_id;
DELETE FROM space_rendition_reservations WHERE source_kind = 'edit';

-- Edit renditions, drawing and deck images, and the room content of drawings, decks and
-- workbooks stop counting toward storage.
UPDATE space_storage_usage u SET used_bytes = GREATEST(0, u.used_bytes - c.bytes), version = u.version + 1, updated_at = now()
FROM (SELECT space_id, sum(logical_bytes) bytes FROM space_storage_contributions
      WHERE state = 'active' AND (source_kind IN ('edit', 'drawing_asset', 'deck_asset')
        OR (source_kind = 'item_content' AND source_id IN (
          SELECT id FROM space_drawings UNION ALL SELECT id FROM space_decks UNION ALL SELECT id FROM space_workbooks)))
      GROUP BY space_id) c
WHERE u.space_id = c.space_id;
DELETE FROM space_storage_contributions
WHERE source_kind IN ('edit', 'drawing_asset', 'deck_asset')
   OR (source_kind = 'item_content' AND source_id IN (
     SELECT id FROM space_drawings UNION ALL SELECT id FROM space_decks UNION ALL SELECT id FROM space_workbooks));

ALTER TABLE space_library_items DROP CONSTRAINT IF EXISTS space_library_items_current_version_fk;
ALTER TABLE space_library_items DROP COLUMN IF EXISTS current_edit_version_id;
-- Its triggers (library_edit_worker_notify, library_rendition_account_notify) go with it.
DROP TABLE IF EXISTS library_item_versions;
DROP TRIGGER IF EXISTS library_reservation_worker_notify ON space_rendition_reservations;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION misty_notify_library_worker() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE item jsonb;
BEGIN
  IF TG_OP = 'DELETE' THEN item := to_jsonb(OLD); ELSE item := to_jsonb(NEW); END IF;
  IF TG_OP = 'UPDATE' AND
    (OLD.state,OLD.available_at,OLD.lease_expires_at,OLD.job_kind,OLD.target_id) IS NOT DISTINCT FROM
    (NEW.state,NEW.available_at,NEW.lease_expires_at,NEW.job_kind,NEW.target_id) THEN RETURN NULL; END IF;
  IF item->>'job_kind' = 'ai' THEN
    PERFORM pg_notify('misty_worker_events','library-ai');
  END IF;
  IF TG_OP = 'UPDATE' AND OLD.job_kind IS DISTINCT FROM NEW.job_kind AND OLD.job_kind = 'ai' THEN
    PERFORM pg_notify('misty_worker_events','library-ai');
  END IF;
  RETURN NULL;
END $$;
-- +goose StatementEnd
DROP TRIGGER library_item_worker_notify ON space_library_items;
CREATE TRIGGER library_item_worker_notify AFTER INSERT OR DELETE OR UPDATE OF lifecycle_state,file_id ON space_library_items
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('library-ai');
DROP TRIGGER library_file_worker_notify ON library_files;
CREATE TRIGGER library_file_worker_notify AFTER INSERT OR DELETE OR UPDATE OF lifecycle_state,blob_id ON library_files
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('library-ai');
DROP TRIGGER library_blob_worker_notify ON library_blobs;
CREATE TRIGGER library_blob_worker_notify AFTER INSERT OR DELETE OR UPDATE OF lifecycle_state ON library_blobs
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('library-ai');

ALTER TABLE library_processing_jobs DROP CONSTRAINT library_processing_jobs_job_kind_check;
ALTER TABLE library_processing_jobs ADD CONSTRAINT library_processing_jobs_job_kind_check CHECK (job_kind = ANY (ARRAY[
  'verify', 'scan', 'preview', 'ocr', 'metadata', 'ai', 'media', 'duplicates', 'export',
  'retention', 'gc', 'quota_reconcile', 'r2_reconcile', 'import']));
ALTER TABLE library_recovery_tombstones DROP CONSTRAINT library_recovery_tombstones_target_kind_check;
ALTER TABLE library_recovery_tombstones ADD CONSTRAINT library_recovery_tombstones_target_kind_check
  CHECK (target_kind = ANY (ARRAY['space_item', 'attachment', 'file', 'blob', 'export', 'import']));
ALTER TABLE space_rendition_reservations DROP CONSTRAINT space_rendition_reservations_source_kind_check;
ALTER TABLE space_rendition_reservations ADD CONSTRAINT space_rendition_reservations_source_kind_check
  CHECK (source_kind = ANY (ARRAY['export', 'preview']));
ALTER TABLE space_storage_contributions DROP CONSTRAINT space_storage_contributions_source_kind_check;
ALTER TABLE space_storage_contributions ADD CONSTRAINT space_storage_contributions_source_kind_check
  CHECK (source_kind IN ('attachment', 'library_item', 'import', 'duplicate', 'export',
    'note_asset', 'item_content', 'drive'));

-- Drawing and deck images were Library uploads of their own purpose, bound to their item.
DELETE FROM space_upload_reservations WHERE upload_id IN (
  SELECT id FROM space_library_uploads WHERE purpose IN ('drawing_attachment', 'deck_attachment'));
DELETE FROM space_library_uploads WHERE purpose IN ('drawing_attachment', 'deck_attachment');
DROP INDEX IF EXISTS space_library_uploads_deck_idx;
DROP INDEX IF EXISTS space_library_uploads_drawing_idx;
ALTER TABLE space_library_uploads
    DROP CONSTRAINT IF EXISTS space_library_uploads_deck_parent_ck,
    DROP CONSTRAINT IF EXISTS space_library_uploads_deck_purpose_ck,
    DROP CONSTRAINT IF EXISTS space_library_uploads_single_parent_ck,
    DROP CONSTRAINT IF EXISTS space_library_uploads_drawing_purpose_ck,
    DROP CONSTRAINT IF EXISTS space_library_uploads_drawing_file_id_ck,
    DROP CONSTRAINT IF EXISTS space_library_uploads_drawing_id_fkey,
    DROP COLUMN IF EXISTS deck_id,
    DROP COLUMN IF EXISTS drawing_file_id,
    DROP COLUMN IF EXISTS drawing_id;
ALTER TABLE space_library_uploads DROP CONSTRAINT space_library_uploads_purpose_check;
ALTER TABLE space_library_uploads ADD CONSTRAINT space_library_uploads_purpose_check
  CHECK (purpose IN ('library', 'attachment', 'note_attachment', 'drive'));

-- What people kept about the removed items: favorites and recents, and the activity about them.
DELETE FROM space_personal_item_state WHERE item_key ~ '^(task|drawing|deck|workbook):';
DELETE FROM space_events WHERE event_type ~ '^(task|calendar|drawing|deck|workbook)\.';

-- The apps' own tables. Their triggers and outboxes go with them.
DROP TABLE IF EXISTS space_workbook_control_outbox;
DROP TABLE IF EXISTS space_workbooks;
DROP TABLE IF EXISTS space_deck_assets;
DROP TABLE IF EXISTS space_deck_control_outbox;
DROP TABLE IF EXISTS space_decks;
DROP TABLE IF EXISTS space_drawing_assets;
DROP TABLE IF EXISTS space_drawing_control_outbox;
DROP TABLE IF EXISTS space_drawings;
DROP TABLE IF EXISTS space_calendar_events;
DROP TABLE IF EXISTS space_tasks;

-- Space apps: no per-app storage, no records permission, and the pinned build of an app outside
-- the reviewed catalog, which the server keeps as given.
DROP TABLE IF EXISTS space_app_storage;
UPDATE space_apps SET permissions = array_remove(permissions, 'records') WHERE 'records' = ANY (permissions);
ALTER TABLE space_apps ADD COLUMN source jsonb
  CHECK (source IS NULL OR (jsonb_typeof(source) = 'object' AND octet_length(source::text) < 65536));

-- +goose Down
-- The schema comes back empty; what the removed apps held is not restored.
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE space_apps DROP COLUMN IF EXISTS source;
CREATE TABLE space_app_storage (
  space_id text NOT NULL,
  app_id text NOT NULL,
  key text NOT NULL CHECK (char_length(key) BETWEEN 1 AND 200),
  value jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (space_id, app_id, key),
  FOREIGN KEY (space_id, app_id) REFERENCES space_apps(space_id, app_id) ON DELETE CASCADE
);
ALTER TABLE space_app_storage ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_app_storage FORCE ROW LEVEL SECURITY;
CREATE POLICY space_app_storage_members ON space_app_storage
  USING (public.misty_rls_is_service() OR public.misty_is_space_member(space_id))
  WITH CHECK (public.misty_rls_is_service() OR public.misty_is_space_member(space_id));

-- Planner.
CREATE TABLE space_tasks (
  id text PRIMARY KEY,
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  task_number bigint NOT NULL,
  title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 240),
  notes text NOT NULL DEFAULT '' CHECK (char_length(notes) <= 20000),
  status text NOT NULL CHECK (status IN ('todo', 'in_progress', 'done', 'canceled')),
  priority text NOT NULL CHECK (priority IN ('high', 'medium', 'low')),
  rank bigint NOT NULL,
  assignee_user_id text REFERENCES users(id) ON DELETE SET NULL,
  assignee_agent_id text NOT NULL DEFAULT '' CHECK (char_length(assignee_agent_id) <= 200),
  due_at timestamptz,
  created_by_user_id text NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  version bigint NOT NULL DEFAULT 1,
  completed_at timestamptz,
  archived_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (space_id, task_number),
  CHECK (assignee_user_id IS NULL OR assignee_agent_id = '')
);
CREATE INDEX space_tasks_board_idx ON space_tasks (space_id, status, rank) WHERE archived_at IS NULL;
CREATE INDEX space_tasks_assignee_idx ON space_tasks (assignee_user_id) WHERE assignee_user_id IS NOT NULL;
CREATE INDEX space_tasks_creator_idx ON space_tasks (created_by_user_id);

CREATE TABLE space_calendar_events (
  id text PRIMARY KEY,
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 240),
  description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 20000),
  location text NOT NULL DEFAULT '' CHECK (char_length(location) <= 500),
  starts_at timestamptz NOT NULL,
  ends_at timestamptz NOT NULL,
  all_day boolean NOT NULL DEFAULT false,
  timezone text NOT NULL DEFAULT 'UTC' CHECK (char_length(timezone) BETWEEN 1 AND 100),
  created_by_user_id text NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (ends_at >= starts_at)
);
CREATE INDEX space_calendar_events_range_idx ON space_calendar_events (space_id, starts_at, ends_at);
CREATE INDEX space_calendar_events_creator_idx ON space_calendar_events (created_by_user_id);

ALTER TABLE space_tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_tasks FORCE ROW LEVEL SECURITY;
CREATE POLICY space_tasks_members ON space_tasks
  USING (public.misty_rls_is_service() OR public.misty_is_space_member(space_id))
  WITH CHECK (public.misty_rls_is_service() OR public.misty_is_space_member(space_id));
ALTER TABLE space_calendar_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_calendar_events FORCE ROW LEVEL SECURITY;
CREATE POLICY space_calendar_events_members ON space_calendar_events
  USING (public.misty_rls_is_service() OR public.misty_is_space_member(space_id))
  WITH CHECK (public.misty_rls_is_service() OR public.misty_is_space_member(space_id));

-- Whiteboards.
CREATE TABLE space_drawings (
  id text PRIMARY KEY,
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  creator_user_id text NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  title text NOT NULL DEFAULT 'Untitled drawing' CONSTRAINT space_drawings_title_check CHECK (char_length(title) BETWEEN 1 AND 200),
  lifecycle_state text NOT NULL DEFAULT 'active' CONSTRAINT space_drawings_lifecycle_state_check CHECK (lifecycle_state IN ('active', 'deleting')),
  collaboration_revision bigint NOT NULL DEFAULT 0,
  acl_version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  audience_kind text NOT NULL DEFAULT 'space' CONSTRAINT space_drawings_audience_kind_check CHECK (audience_kind IN ('space', 'conversation')),
  audience_conversation_id text REFERENCES space_conversations(id) ON DELETE CASCADE,
  -- No default: misty_item_default_visibility gives a new drawing the Space's.
  visibility text NOT NULL CHECK (visibility IN ('public', 'private')),
  content_bytes bigint NOT NULL DEFAULT 0 CHECK (content_bytes >= 0),
  history_bytes bigint NOT NULL DEFAULT 0 CHECK (history_bytes >= 0),
  plain_text_projection text NOT NULL DEFAULT '' CHECK (char_length(plain_text_projection) <= 100000),
  search_tsv tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple'::regconfig, title), 'A')
    || setweight(to_tsvector('simple'::regconfig, plain_text_projection), 'C')
  ) STORED,
  CONSTRAINT space_drawings_audience_check CHECK ((audience_kind = 'space' AND audience_conversation_id IS NULL)
      OR (audience_kind = 'conversation' AND audience_conversation_id IS NOT NULL))
);
CREATE INDEX space_drawings_creator_idx ON space_drawings (creator_user_id, lifecycle_state);
CREATE INDEX space_drawings_space_recent_idx ON space_drawings (space_id, lifecycle_state, updated_at DESC);
CREATE INDEX space_drawings_search_idx ON space_drawings USING gin (search_tsv);
CREATE TRIGGER space_drawings_default_visibility BEFORE INSERT ON space_drawings
  FOR EACH ROW EXECUTE FUNCTION misty_item_default_visibility();
CREATE TRIGGER space_drawings_visibility_guard BEFORE UPDATE OF visibility ON space_drawings
  FOR EACH ROW EXECUTE FUNCTION misty_item_visibility_guard('creator_user_id');
ALTER TABLE space_drawings ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_drawings FORCE ROW LEVEL SECURITY;
CREATE POLICY space_drawings_audience_policy ON space_drawings
  USING (misty_rls_is_service() OR (lifecycle_state = 'active'
      AND misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)
      AND (visibility = 'public' OR creator_user_id = misty_rls_user_id())))
  WITH CHECK (misty_rls_is_service() OR (creator_user_id = misty_rls_user_id()
      AND misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)));
CREATE TRIGGER drawing_purge_worker_notify AFTER INSERT OR DELETE OR UPDATE OF lifecycle_state ON space_drawings
  FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('drawing-purge');

CREATE TABLE space_drawing_control_outbox (
  id text PRIMARY KEY,
  drawing_id text NOT NULL REFERENCES space_drawings(id) ON DELETE CASCADE,
  command text NOT NULL CONSTRAINT space_drawing_control_outbox_command_check CHECK (command IN ('acl', 'disconnect', 'purge')),
  payload jsonb NOT NULL DEFAULT '{}'::jsonb,
  attempts integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  last_error text NOT NULL DEFAULT '',
  delivered_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX space_drawing_control_outbox_pending_idx
  ON space_drawing_control_outbox (next_attempt_at) WHERE delivered_at IS NULL;
ALTER TABLE space_drawing_control_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_drawing_control_outbox FORCE ROW LEVEL SECURITY;
CREATE POLICY space_drawing_control_outbox_policy ON space_drawing_control_outbox
  USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
CREATE TRIGGER drawing_control_worker_notify
  AFTER INSERT OR DELETE OR UPDATE OF next_attempt_at,delivered_at ON space_drawing_control_outbox
  FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('drawing-control', 'drawing-purge');

CREATE TABLE space_drawing_assets (
  id text PRIMARY KEY,
  drawing_id text NOT NULL REFERENCES space_drawings(id) ON DELETE CASCADE,
  file_id text NOT NULL REFERENCES library_files(id) ON DELETE RESTRICT,
  uploader_user_id text NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  excalidraw_file_id text NOT NULL CONSTRAINT space_drawing_assets_excalidraw_file_id_check CHECK (char_length(excalidraw_file_id) BETWEEN 1 AND 160),
  display_name text NOT NULL CONSTRAINT space_drawing_assets_display_name_check CHECK (char_length(display_name) BETWEEN 1 AND 255),
  lifecycle_state text NOT NULL DEFAULT 'ready' CONSTRAINT space_drawing_assets_lifecycle_state_check
    CHECK (lifecycle_state IN ('ready', 'unreferenced', 'deleting', 'deleted')),
  created_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz,
  CONSTRAINT space_drawing_assets_drawing_id_excalidraw_file_id_key UNIQUE (drawing_id, excalidraw_file_id)
);
CREATE INDEX space_drawing_assets_cleanup_idx ON space_drawing_assets (lifecycle_state, deleted_at)
  WHERE lifecycle_state <> 'ready';
CREATE INDEX space_drawing_assets_drawing_idx ON space_drawing_assets (drawing_id, lifecycle_state);
ALTER TABLE space_drawing_assets ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_drawing_assets FORCE ROW LEVEL SECURITY;
CREATE POLICY space_drawing_assets_access_policy ON space_drawing_assets
  USING (misty_rls_is_service() OR EXISTS (
    SELECT 1 FROM space_drawings d JOIN space_members m ON m.space_id = d.space_id
    WHERE d.id = space_drawing_assets.drawing_id AND d.lifecycle_state = 'active' AND m.user_id = misty_rls_user_id()))
  WITH CHECK (misty_rls_is_service() OR EXISTS (
    SELECT 1 FROM space_drawings d JOIN space_members m ON m.space_id = d.space_id
    WHERE d.id = space_drawing_assets.drawing_id AND d.lifecycle_state = 'active' AND m.user_id = misty_rls_user_id()));

-- Slides.
CREATE TABLE space_decks (
  id text PRIMARY KEY,
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  creator_user_id text NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  title text NOT NULL DEFAULT 'Untitled deck' CHECK (char_length(title) BETWEEN 1 AND 200),
  lifecycle_state text NOT NULL DEFAULT 'active' CHECK (lifecycle_state IN ('active', 'deleting')),
  collaboration_revision bigint NOT NULL DEFAULT 0,
  acl_version bigint NOT NULL DEFAULT 1 CHECK (acl_version >= 1),
  audience_kind text NOT NULL DEFAULT 'space' CHECK (audience_kind IN ('space', 'conversation')),
  audience_conversation_id text REFERENCES space_conversations(id) ON DELETE CASCADE,
  visibility text NOT NULL CHECK (visibility IN ('public', 'private')),
  plain_text_projection text NOT NULL DEFAULT '' CHECK (char_length(plain_text_projection) <= 100000),
  content_bytes bigint NOT NULL DEFAULT 0 CHECK (content_bytes >= 0),
  history_bytes bigint NOT NULL DEFAULT 0 CHECK (history_bytes >= 0),
  slide_count integer NOT NULL DEFAULT 0 CHECK (slide_count >= 0),
  search_tsv tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple'::regconfig, title), 'A')
    || setweight(to_tsvector('simple'::regconfig, plain_text_projection), 'C')
  ) STORED,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((audience_kind = 'space' AND audience_conversation_id IS NULL)
      OR (audience_kind = 'conversation' AND audience_conversation_id IS NOT NULL))
);
CREATE INDEX space_decks_space_recent_idx ON space_decks(space_id, lifecycle_state, updated_at DESC);
CREATE INDEX space_decks_creator_idx ON space_decks(creator_user_id, lifecycle_state);
CREATE INDEX space_decks_private_owner ON space_decks(space_id, creator_user_id) WHERE visibility = 'private';
CREATE INDEX space_decks_search_idx ON space_decks USING gin (search_tsv);
CREATE TRIGGER space_decks_default_visibility BEFORE INSERT ON space_decks
  FOR EACH ROW EXECUTE FUNCTION misty_item_default_visibility();
CREATE TRIGGER space_decks_visibility_guard BEFORE UPDATE OF visibility ON space_decks
  FOR EACH ROW EXECUTE FUNCTION misty_item_visibility_guard('creator_user_id');
ALTER TABLE space_decks ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_decks FORCE ROW LEVEL SECURITY;
CREATE POLICY space_decks_audience_policy ON space_decks
  USING (misty_rls_is_service() OR (lifecycle_state = 'active'
      AND misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)
      AND (visibility = 'public' OR creator_user_id = misty_rls_user_id())))
  WITH CHECK (misty_rls_is_service() OR (creator_user_id = misty_rls_user_id()
      AND misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)));

CREATE TABLE space_deck_control_outbox (
  id text PRIMARY KEY,
  deck_id text NOT NULL REFERENCES space_decks(id) ON DELETE CASCADE,
  command text NOT NULL CHECK (command IN ('acl', 'disconnect', 'title', 'purge')),
  payload jsonb NOT NULL DEFAULT '{}'::jsonb,
  attempts integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  last_error text NOT NULL DEFAULT '',
  delivered_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX space_deck_control_outbox_pending_idx
  ON space_deck_control_outbox(next_attempt_at) WHERE delivered_at IS NULL;
CREATE INDEX space_deck_control_outbox_deck_idx ON space_deck_control_outbox(deck_id, command);
ALTER TABLE space_deck_control_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_deck_control_outbox FORCE ROW LEVEL SECURITY;
CREATE POLICY space_deck_control_outbox_service ON space_deck_control_outbox
  USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
CREATE TRIGGER deck_control_worker_notify
  AFTER INSERT OR DELETE OR UPDATE OF next_attempt_at,delivered_at ON space_deck_control_outbox
  FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('deck-control', 'deck-purge');
CREATE TRIGGER deck_purge_worker_notify
  AFTER INSERT OR DELETE OR UPDATE OF lifecycle_state ON space_decks
  FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('deck-purge');

CREATE TABLE space_deck_assets (
  id text PRIMARY KEY,
  deck_id text NOT NULL REFERENCES space_decks(id) ON DELETE CASCADE,
  file_id text NOT NULL REFERENCES library_files(id) ON DELETE RESTRICT,
  uploader_user_id text NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  display_name text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 255),
  lifecycle_state text NOT NULL DEFAULT 'ready'
    CHECK (lifecycle_state IN ('ready', 'unreferenced', 'deleting', 'deleted')),
  created_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz
);
CREATE INDEX space_deck_assets_deck_idx ON space_deck_assets(deck_id, lifecycle_state);
CREATE TRIGGER deck_asset_purge_worker_notify
  AFTER DELETE OR UPDATE OF lifecycle_state ON space_deck_assets
  FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('deck-purge');
CREATE INDEX space_deck_assets_file_idx ON space_deck_assets(file_id);
CREATE INDEX space_deck_assets_cleanup_idx ON space_deck_assets(lifecycle_state, deleted_at)
  WHERE lifecycle_state <> 'ready';
ALTER TABLE space_deck_assets ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_deck_assets FORCE ROW LEVEL SECURITY;
CREATE POLICY space_deck_assets_access_policy ON space_deck_assets
  USING (public.misty_rls_is_service() OR EXISTS (
    SELECT 1 FROM space_decks d JOIN space_members m ON m.space_id = d.space_id
    WHERE d.id = space_deck_assets.deck_id AND d.lifecycle_state = 'active' AND m.user_id = misty_rls_user_id()))
  WITH CHECK (misty_rls_is_service() OR EXISTS (
    SELECT 1 FROM space_decks d JOIN space_members m ON m.space_id = d.space_id
    WHERE d.id = space_deck_assets.deck_id AND d.lifecycle_state = 'active' AND m.user_id = misty_rls_user_id()));

-- Data.
CREATE TABLE space_workbooks (
  id text PRIMARY KEY,
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  creator_user_id text NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  title text NOT NULL DEFAULT 'Untitled data' CHECK (char_length(title) BETWEEN 1 AND 200),
  lifecycle_state text NOT NULL DEFAULT 'active' CHECK (lifecycle_state IN ('active', 'deleting')),
  collaboration_revision bigint NOT NULL DEFAULT 0,
  acl_version bigint NOT NULL DEFAULT 1 CHECK (acl_version >= 1),
  audience_kind text NOT NULL DEFAULT 'space' CHECK (audience_kind IN ('space', 'conversation')),
  audience_conversation_id text REFERENCES space_conversations(id) ON DELETE CASCADE,
  visibility text NOT NULL CHECK (visibility IN ('public', 'private')),
  plain_text_projection text NOT NULL DEFAULT '' CHECK (char_length(plain_text_projection) <= 100000),
  content_bytes bigint NOT NULL DEFAULT 0 CHECK (content_bytes >= 0),
  history_bytes bigint NOT NULL DEFAULT 0 CHECK (history_bytes >= 0),
  cell_count integer NOT NULL DEFAULT 0 CHECK (cell_count >= 0),
  search_tsv tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple'::regconfig, title), 'A')
    || setweight(to_tsvector('simple'::regconfig, plain_text_projection), 'C')
  ) STORED,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((audience_kind = 'space' AND audience_conversation_id IS NULL)
      OR (audience_kind = 'conversation' AND audience_conversation_id IS NOT NULL))
);
CREATE INDEX space_workbooks_space_recent_idx ON space_workbooks(space_id, lifecycle_state, updated_at DESC);
CREATE INDEX space_workbooks_creator_idx ON space_workbooks(creator_user_id, lifecycle_state);
CREATE INDEX space_workbooks_private_owner ON space_workbooks(space_id, creator_user_id) WHERE visibility = 'private';
CREATE INDEX space_workbooks_search_idx ON space_workbooks USING gin (search_tsv);
CREATE TRIGGER space_workbooks_default_visibility BEFORE INSERT ON space_workbooks
  FOR EACH ROW EXECUTE FUNCTION misty_item_default_visibility();
CREATE TRIGGER space_workbooks_visibility_guard BEFORE UPDATE OF visibility ON space_workbooks
  FOR EACH ROW EXECUTE FUNCTION misty_item_visibility_guard('creator_user_id');
ALTER TABLE space_workbooks ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_workbooks FORCE ROW LEVEL SECURITY;
CREATE POLICY space_workbooks_audience_policy ON space_workbooks
  USING (misty_rls_is_service() OR (lifecycle_state = 'active'
      AND misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)
      AND (visibility = 'public' OR creator_user_id = misty_rls_user_id())))
  WITH CHECK (misty_rls_is_service() OR (creator_user_id = misty_rls_user_id()
      AND misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)));

CREATE TABLE space_workbook_control_outbox (
  id text PRIMARY KEY,
  workbook_id text NOT NULL REFERENCES space_workbooks(id) ON DELETE CASCADE,
  command text NOT NULL CHECK (command IN ('acl', 'disconnect', 'title', 'purge')),
  payload jsonb NOT NULL DEFAULT '{}'::jsonb,
  attempts integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  last_error text NOT NULL DEFAULT '',
  delivered_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX space_workbook_control_outbox_pending_idx
  ON space_workbook_control_outbox(next_attempt_at) WHERE delivered_at IS NULL;
CREATE INDEX space_workbook_control_outbox_workbook_idx ON space_workbook_control_outbox(workbook_id, command);
ALTER TABLE space_workbook_control_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_workbook_control_outbox FORCE ROW LEVEL SECURITY;
CREATE POLICY space_workbook_control_outbox_service ON space_workbook_control_outbox
  USING (misty_rls_is_service()) WITH CHECK (misty_rls_is_service());
CREATE TRIGGER workbook_control_worker_notify
  AFTER INSERT OR DELETE OR UPDATE OF next_attempt_at,delivered_at ON space_workbook_control_outbox
  FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('workbook-control', 'workbook-purge');
CREATE TRIGGER workbook_purge_worker_notify
  AFTER INSERT OR DELETE OR UPDATE OF lifecycle_state ON space_workbooks
  FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('workbook-purge');

-- The Library: uploads bound to a drawing or a deck, and edit versions with their renditions.
ALTER TABLE space_library_uploads DROP CONSTRAINT space_library_uploads_purpose_check;
ALTER TABLE space_library_uploads ADD CONSTRAINT space_library_uploads_purpose_check
  CHECK (purpose IN ('library', 'attachment', 'note_attachment', 'drawing_attachment', 'deck_attachment', 'drive'));
ALTER TABLE space_library_uploads
    ADD COLUMN drawing_id text,
    ADD COLUMN drawing_file_id text,
    ADD COLUMN deck_id text REFERENCES space_decks(id) ON DELETE CASCADE,
    ADD CONSTRAINT space_library_uploads_drawing_id_fkey FOREIGN KEY (drawing_id) REFERENCES space_drawings(id) ON DELETE CASCADE,
    ADD CONSTRAINT space_library_uploads_drawing_file_id_ck CHECK (((drawing_file_id IS NULL) OR ((char_length(drawing_file_id) >= 1) AND (char_length(drawing_file_id) <= 160)))),
    ADD CONSTRAINT space_library_uploads_drawing_purpose_ck CHECK (((purpose = 'drawing_attachment'::text) = ((drawing_id IS NOT NULL) AND (drawing_file_id IS NOT NULL)))),
    ADD CONSTRAINT space_library_uploads_single_parent_ck CHECK ((NOT ((note_id IS NOT NULL) AND (drawing_id IS NOT NULL)))),
    ADD CONSTRAINT space_library_uploads_deck_purpose_ck CHECK ((purpose = 'deck_attachment') = (deck_id IS NOT NULL)),
    ADD CONSTRAINT space_library_uploads_deck_parent_ck CHECK (deck_id IS NULL OR (note_id IS NULL AND drawing_id IS NULL));
CREATE INDEX space_library_uploads_drawing_idx ON space_library_uploads USING btree (drawing_id) WHERE (drawing_id IS NOT NULL);
CREATE INDEX space_library_uploads_deck_idx ON space_library_uploads(deck_id) WHERE deck_id IS NOT NULL;

ALTER TABLE space_storage_contributions DROP CONSTRAINT space_storage_contributions_source_kind_check;
ALTER TABLE space_storage_contributions ADD CONSTRAINT space_storage_contributions_source_kind_check
  CHECK (source_kind IN ('attachment', 'library_item', 'import', 'duplicate', 'edit', 'export',
    'note_asset', 'drawing_asset', 'deck_asset', 'item_content', 'drive'));
ALTER TABLE space_rendition_reservations DROP CONSTRAINT space_rendition_reservations_source_kind_check;
ALTER TABLE space_rendition_reservations ADD CONSTRAINT space_rendition_reservations_source_kind_check
  CHECK ((source_kind = ANY (ARRAY['edit'::text, 'export'::text, 'preview'::text])));
ALTER TABLE library_recovery_tombstones DROP CONSTRAINT library_recovery_tombstones_target_kind_check;
ALTER TABLE library_recovery_tombstones ADD CONSTRAINT library_recovery_tombstones_target_kind_check
  CHECK ((target_kind = ANY (ARRAY['space_item'::text, 'attachment'::text, 'edit'::text, 'file'::text, 'blob'::text, 'export'::text, 'import'::text])));
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

CREATE TABLE library_item_versions (
    id text NOT NULL,
    space_library_item_id text NOT NULL,
    parent_version_id text,
    created_by_user_id text NOT NULL,
    rendition_blob_id text,
    edit_definition jsonb DEFAULT '{}'::jsonb NOT NULL,
    lifecycle_state text DEFAULT 'ready'::text NOT NULL,
    version_number bigint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    deleted_at timestamp with time zone,
    rendition_state text DEFAULT 'none'::text NOT NULL,
    rendition_mime_type text DEFAULT ''::text NOT NULL,
    rendition_byte_size bigint,
    rendition_error_code text DEFAULT ''::text NOT NULL,
    rendition_updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT library_item_versions_pkey PRIMARY KEY (id),
    CONSTRAINT library_item_versions_space_library_item_id_version_number_key UNIQUE (space_library_item_id, version_number),
    CONSTRAINT library_item_versions_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT library_item_versions_parent_version_id_fkey FOREIGN KEY (parent_version_id) REFERENCES library_item_versions(id) ON DELETE RESTRICT,
    CONSTRAINT library_item_versions_rendition_blob_id_fkey FOREIGN KEY (rendition_blob_id) REFERENCES library_blobs(id) ON DELETE RESTRICT,
    CONSTRAINT library_item_versions_space_library_item_id_fkey FOREIGN KEY (space_library_item_id) REFERENCES space_library_items(id) ON DELETE CASCADE,
    CONSTRAINT library_item_versions_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['ready'::text, 'recovery'::text, 'purging'::text, 'deleted'::text]))),
    CONSTRAINT library_item_versions_rendition_byte_size_check CHECK (((rendition_byte_size IS NULL) OR (rendition_byte_size > 0))),
    CONSTRAINT library_item_versions_rendition_state_check CHECK ((rendition_state = ANY (ARRAY['none'::text, 'queued'::text, 'processing'::text, 'ready'::text, 'failed'::text])))
);
ALTER TABLE library_item_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE ONLY library_item_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY library_item_versions_policy ON library_item_versions USING ((misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM space_library_items i
  WHERE ((i.id = library_item_versions.space_library_item_id) AND misty_is_space_member(i.space_id)))))) WITH CHECK ((misty_rls_is_service() OR (EXISTS ( SELECT 1
   FROM space_library_items i
  WHERE ((i.id = library_item_versions.space_library_item_id) AND misty_is_space_member(i.space_id))))));
CREATE TRIGGER library_edit_worker_notify AFTER INSERT OR DELETE OR UPDATE OF lifecycle_state ON library_item_versions
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('library-edit');
CREATE TRIGGER library_rendition_account_notify AFTER UPDATE OF rendition_state ON library_item_versions
FOR EACH ROW WHEN (OLD.rendition_state IS DISTINCT FROM NEW.rendition_state)
EXECUTE FUNCTION misty_notify_account_change('library-renditions','created_by_user_id');
CREATE TRIGGER library_reservation_worker_notify AFTER INSERT OR DELETE OR UPDATE OF state ON space_rendition_reservations
FOR EACH ROW EXECUTE FUNCTION misty_notify_worker_queue('library-edit');

ALTER TABLE space_library_items ADD COLUMN current_edit_version_id text;
ALTER TABLE space_library_items ADD CONSTRAINT space_library_items_current_version_fk
  FOREIGN KEY (current_edit_version_id) REFERENCES library_item_versions(id) ON DELETE SET NULL;

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON
          space_app_storage, space_tasks, space_calendar_events,
          space_drawings, space_drawing_control_outbox, space_drawing_assets,
          space_decks, space_deck_control_outbox, space_deck_assets,
          space_workbooks, space_workbook_control_outbox, library_item_versions
        TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd
