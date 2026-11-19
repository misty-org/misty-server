-- +goose Up
-- Two more kinds of thing a Space holds, beside notes and drawings: decks (Slides) and workbooks
-- (Data). Each is a collaborative Yjs document in its own room of the collaboration Worker
-- (deck-room, workbook-room), exactly as a drawing is: this row is its server-owned record (title,
-- audience, layer, ACL version) and its projection (the plain text search reads, the size its room
-- reports). Deleting one purges its room through the control outbox, and the row goes once the
-- room confirms.
--
-- Every made item (notes and drawings too) now records the size of its room's snapshot and kept
-- history, which count toward storage as an 'item_content' contribution. Drawings get the plain
-- text of their text elements for search.
SELECT set_config('app.rls_mode', 'service', true);

-- Decks.
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
  -- No default: misty_item_default_visibility gives a new deck the Space's.
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

-- Retryable commands for a deck's room: a new ACL version, a renamed title, the purge.
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

-- A deck's images: R2-backed files its slides reference by asset id, as a drawing's are. Image
-- bytes never enter PostgreSQL or the room.
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
-- A deleted deck's row waits for its images' cleanup; their last step wakes its purge.
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

-- Workbooks.
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

-- A deck's image uploads travel as Library uploads of their own purpose, bound to the deck.
ALTER TABLE space_library_uploads DROP CONSTRAINT space_library_uploads_purpose_check;
ALTER TABLE space_library_uploads ADD CONSTRAINT space_library_uploads_purpose_check
  CHECK (purpose IN ('library', 'attachment', 'note_attachment', 'drawing_attachment', 'folder', 'deck_attachment'));
ALTER TABLE space_library_uploads ADD COLUMN deck_id text REFERENCES space_decks(id) ON DELETE CASCADE;
ALTER TABLE space_library_uploads ADD CONSTRAINT space_library_uploads_deck_purpose_ck
  CHECK ((purpose = 'deck_attachment') = (deck_id IS NOT NULL));
ALTER TABLE space_library_uploads ADD CONSTRAINT space_library_uploads_deck_parent_ck
  CHECK (deck_id IS NULL OR (note_id IS NULL AND drawing_id IS NULL));
CREATE INDEX space_library_uploads_deck_idx ON space_library_uploads(deck_id) WHERE deck_id IS NOT NULL;

-- A deck's images and every made item's room content count toward storage.
ALTER TABLE space_storage_contributions DROP CONSTRAINT space_storage_contributions_source_kind_check;
ALTER TABLE space_storage_contributions ADD CONSTRAINT space_storage_contributions_source_kind_check
  CHECK (source_kind IN ('attachment', 'library_item', 'import', 'duplicate', 'edit', 'export',
    'note_asset', 'drawing_asset', 'shared_folder', 'deck_asset', 'item_content'));

-- Notes and drawings report their room's size too, and drawings their text for search.
ALTER TABLE space_notes ADD COLUMN content_bytes bigint NOT NULL DEFAULT 0 CHECK (content_bytes >= 0);
ALTER TABLE space_notes ADD COLUMN history_bytes bigint NOT NULL DEFAULT 0 CHECK (history_bytes >= 0);
ALTER TABLE space_drawings ADD COLUMN content_bytes bigint NOT NULL DEFAULT 0 CHECK (content_bytes >= 0);
ALTER TABLE space_drawings ADD COLUMN history_bytes bigint NOT NULL DEFAULT 0 CHECK (history_bytes >= 0);
ALTER TABLE space_drawings ADD COLUMN plain_text_projection text NOT NULL DEFAULT ''
  CHECK (char_length(plain_text_projection) <= 100000);
ALTER TABLE space_drawings ADD COLUMN search_tsv tsvector GENERATED ALWAYS AS (
  setweight(to_tsvector('simple'::regconfig, title), 'A')
  || setweight(to_tsvector('simple'::regconfig, plain_text_projection), 'C')
) STORED;
CREATE INDEX space_drawings_search_idx ON space_drawings USING gin (search_tsv);

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON
          space_decks, space_deck_control_outbox, space_deck_assets,
          space_workbooks, space_workbook_control_outbox
        TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);

DROP INDEX IF EXISTS space_drawings_search_idx;
ALTER TABLE space_drawings DROP COLUMN IF EXISTS search_tsv;
ALTER TABLE space_drawings DROP COLUMN IF EXISTS plain_text_projection;
ALTER TABLE space_drawings DROP COLUMN IF EXISTS history_bytes;
ALTER TABLE space_drawings DROP COLUMN IF EXISTS content_bytes;
ALTER TABLE space_notes DROP COLUMN IF EXISTS history_bytes;
ALTER TABLE space_notes DROP COLUMN IF EXISTS content_bytes;

DELETE FROM space_storage_contributions WHERE source_kind IN ('deck_asset', 'item_content');
ALTER TABLE space_storage_contributions DROP CONSTRAINT space_storage_contributions_source_kind_check;
ALTER TABLE space_storage_contributions ADD CONSTRAINT space_storage_contributions_source_kind_check
  CHECK (source_kind IN ('attachment', 'library_item', 'import', 'duplicate', 'edit', 'export',
    'note_asset', 'drawing_asset', 'shared_folder'));

DELETE FROM space_library_uploads WHERE purpose = 'deck_attachment';
DROP INDEX IF EXISTS space_library_uploads_deck_idx;
ALTER TABLE space_library_uploads DROP CONSTRAINT IF EXISTS space_library_uploads_deck_parent_ck;
ALTER TABLE space_library_uploads DROP CONSTRAINT IF EXISTS space_library_uploads_deck_purpose_ck;
ALTER TABLE space_library_uploads DROP COLUMN IF EXISTS deck_id;
ALTER TABLE space_library_uploads DROP CONSTRAINT space_library_uploads_purpose_check;
ALTER TABLE space_library_uploads ADD CONSTRAINT space_library_uploads_purpose_check
  CHECK (purpose IN ('library', 'attachment', 'note_attachment', 'drawing_attachment', 'folder'));

DROP TABLE IF EXISTS space_workbook_control_outbox;
DROP TABLE IF EXISTS space_workbooks;
DROP TABLE IF EXISTS space_deck_assets;
DROP TABLE IF EXISTS space_deck_control_outbox;
DROP TABLE IF EXISTS space_decks;
