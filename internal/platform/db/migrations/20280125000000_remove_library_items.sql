-- +goose Up
-- A Space's Library items (its Media) go: the Space drive holds a Space's files now. The items
-- and what only they had go with them: their views, the messages' references to them, the files
-- agents saved as items, their preview reservations and links to them from other Spaces, and the
-- item columns on uploads, attachments and derivatives. Their bytes stop counting: each item's
-- storage contribution is released, an item upload still in flight gives its reservation back,
-- and the files, previews and blobs only items used are retired, so the Library's orphan sweep
-- deletes their objects. Chat attachments, note images, avatars and the drive keep theirs. The
-- Library's permissions only gated items, so roles lose them. There are no real users yet, so
-- nothing moves into the drive.
SELECT set_config('app.rls_mode', 'service', true);

-- Uploads that were going to become items are given up.
UPDATE space_upload_reservations SET state = 'released', updated_at = now()
WHERE state = 'active' AND upload_id IN (SELECT id FROM space_library_uploads WHERE purpose = 'library');
UPDATE space_library_uploads SET state = 'expired', error_code = 'library_items_removed', version = version + 1, updated_at = now()
WHERE purpose = 'library' AND state IN ('initiated', 'uploading', 'uploaded_unverified', 'quarantined', 'scanning', 'processing');

-- The items' bytes, their copies' and their imports'.
UPDATE space_storage_contributions SET state = 'released', released_at = now(), updated_at = now()
WHERE source_kind IN ('library_item', 'import', 'duplicate') AND state IN ('active', 'recovery');

-- What may now be unused, checked once everything that could still use it is retired.
CREATE TEMP TABLE misty_items_retired_blobs (blob_id text PRIMARY KEY);

-- Previews made for items.
WITH gone AS (
  UPDATE library_derivatives SET lifecycle_state = 'deleted', updated_at = now()
  WHERE space_library_item_id IS NOT NULL AND lifecycle_state <> 'deleted'
  RETURNING derivative_blob_id
)
INSERT INTO misty_items_retired_blobs SELECT DISTINCT derivative_blob_id FROM gone WHERE derivative_blob_id IS NOT NULL
ON CONFLICT DO NOTHING;

-- Files only items used, unless something holds them, and the files' own derivatives.
CREATE TEMP TABLE misty_items_retired_files AS
SELECT f.id, f.blob_id FROM library_files f
WHERE f.lifecycle_state = 'ready'
  AND EXISTS (SELECT 1 FROM space_library_items i WHERE i.file_id = f.id)
  AND NOT EXISTS (SELECT 1 FROM library_legal_holds h WHERE h.active AND h.target_kind = 'file' AND h.target_id = f.id)
  AND NOT EXISTS (SELECT 1 FROM library_legal_holds h JOIN space_library_items i ON i.id = h.target_id
                  WHERE h.active AND h.target_kind = 'space_item' AND i.file_id = f.id)
  AND NOT EXISTS (SELECT 1 FROM space_message_attachments a WHERE a.file_id = f.id AND a.lifecycle_state <> 'deleted')
  AND NOT EXISTS (SELECT 1 FROM space_note_assets a WHERE a.file_id = f.id AND a.lifecycle_state <> 'deleted');
UPDATE library_files f SET lifecycle_state = 'deleted', deleted_at = now(), version = f.version + 1, updated_at = now()
FROM misty_items_retired_files r WHERE f.id = r.id;
INSERT INTO misty_items_retired_blobs SELECT DISTINCT blob_id FROM misty_items_retired_files ON CONFLICT DO NOTHING;
WITH gone AS (
  UPDATE library_derivatives d SET lifecycle_state = 'deleted', updated_at = now()
  FROM misty_items_retired_files r
  WHERE d.source_file_id = r.id AND d.lifecycle_state <> 'deleted'
  RETURNING d.derivative_blob_id
)
INSERT INTO misty_items_retired_blobs SELECT DISTINCT derivative_blob_id FROM gone WHERE derivative_blob_id IS NOT NULL
ON CONFLICT DO NOTHING;

-- A blob nothing live uses any more is retired, with the finished upload that first wrote its
-- object, so the orphan sweep collects the object (retireLibraryBlobsIfUnusedTx's rule).
WITH gone AS (
  UPDATE library_blobs b SET lifecycle_state = 'deleted', deleted_at = now(), version = b.version + 1, updated_at = now()
  FROM misty_items_retired_blobs r
  WHERE b.id = r.blob_id AND b.lifecycle_state = 'ready'
    AND NOT EXISTS (SELECT 1 FROM library_legal_holds h WHERE h.active AND h.target_kind = 'blob' AND h.target_id = b.id)
    AND NOT EXISTS (SELECT 1 FROM library_files f WHERE f.blob_id = b.id AND f.lifecycle_state NOT IN ('purging', 'deleted'))
    AND NOT EXISTS (SELECT 1 FROM library_derivatives d WHERE d.derivative_blob_id = b.id AND d.lifecycle_state <> 'deleted')
    AND NOT EXISTS (SELECT 1 FROM library_exports e WHERE e.export_blob_id = b.id AND e.state <> 'deleted')
    AND NOT EXISTS (SELECT 1 FROM space_drive_items di WHERE di.blob_key = b.r2_object_key)
  RETURNING b.r2_object_key
)
UPDATE space_library_uploads u SET state = 'deleted', version = u.version + 1, updated_at = now()
FROM gone WHERE u.object_key = gone.r2_object_key AND u.state = 'ready';

DROP TABLE misty_items_retired_files;
DROP TABLE misty_items_retired_blobs;

-- Names, jobs and the Library's permissions that only items had.
DELETE FROM space_item_aliases WHERE target_kind IN ('library_item', 'direct_reference');
DELETE FROM library_processing_jobs WHERE target_kind IN ('space_item', 'library_item');
UPDATE space_roles SET permissions = ARRAY(
    SELECT p FROM unnest(permissions) WITH ORDINALITY AS t(p, n)
    WHERE p NOT IN ('library.view', 'library.upload', 'library.add', 'library.edit', 'library.download', 'library.import')
    ORDER BY n),
  updated_at = now()
WHERE permissions && ARRAY['library.view', 'library.upload', 'library.add', 'library.edit', 'library.download', 'library.import']::text[];

-- The item columns elsewhere, then the item tables.
ALTER TABLE space_message_attachments DROP COLUMN promoted_item_id;
ALTER TABLE library_derivatives DROP COLUMN space_library_item_id;
ALTER TABLE space_library_uploads DROP CONSTRAINT IF EXISTS space_library_uploads_replace_ck;
ALTER TABLE space_library_uploads
  DROP COLUMN agent_id,
  DROP COLUMN agent_conversation_id,
  DROP COLUMN agent_run_id,
  DROP COLUMN agent_artifact_kind,
  DROP COLUMN replace_item_id,
  DROP COLUMN replace_item_version;
DROP TABLE space_message_library_references;
DROP TABLE space_agent_artifacts;
DROP TABLE space_library_item_views;
DROP TABLE space_library_direct_references;
DROP TABLE space_rendition_reservations;
DROP TABLE space_library_items;

-- Every Space's counters, from what is left.
UPDATE space_storage_usage u SET used_bytes = a.used, reserved_bytes = a.reserved, version = u.version + 1, updated_at = now()
FROM (
  SELECT s.space_id,
    COALESCE((SELECT sum(c.logical_bytes) FROM space_storage_contributions c
              WHERE c.space_id = s.space_id AND c.state IN ('active', 'recovery')), 0) AS used,
    COALESCE((SELECT sum(r.reserved_bytes) FROM space_upload_reservations r
              WHERE r.space_id = s.space_id AND r.state = 'active'), 0) AS reserved
  FROM space_storage_usage s
) a
WHERE u.space_id = a.space_id AND (u.used_bytes <> a.used OR u.reserved_bytes <> a.reserved);

-- +goose Down
-- The schema comes back empty; the removed items, and what they held, are not restored.
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE space_library_items (
  id text NOT NULL,
  space_id text NOT NULL,
  file_id text NOT NULL,
  contributing_user_id text NOT NULL,
  display_name text NOT NULL,
  caption text DEFAULT ''::text NOT NULL,
  tags jsonb DEFAULT '[]'::jsonb NOT NULL,
  favorite boolean DEFAULT false NOT NULL,
  hidden boolean DEFAULT false NOT NULL,
  date_override timestamp with time zone,
  location_override jsonb,
  contributor_information jsonb DEFAULT '{}'::jsonb NOT NULL,
  added_by_user_id text NOT NULL,
  lifecycle_state text DEFAULT 'ready'::text NOT NULL,
  added_at timestamp with time zone DEFAULT now() NOT NULL,
  trashed_at timestamp with time zone,
  recover_until timestamp with time zone,
  version bigint DEFAULT 1 NOT NULL,
  updated_at timestamp with time zone DEFAULT now() NOT NULL,
  audience_kind text DEFAULT 'space'::text NOT NULL,
  audience_conversation_id text,
  visibility text NOT NULL,
  CONSTRAINT space_library_items_pkey PRIMARY KEY (id),
  CONSTRAINT space_library_items_audience_check CHECK ((((audience_kind = 'space'::text) AND (audience_conversation_id IS NULL)) OR ((audience_kind = 'conversation'::text) AND (audience_conversation_id IS NOT NULL)))),
  CONSTRAINT space_library_items_audience_kind_check CHECK ((audience_kind = ANY (ARRAY['space'::text, 'conversation'::text]))),
  CONSTRAINT space_library_items_caption_check CHECK ((char_length(caption) <= 4000)),
  CONSTRAINT space_library_items_display_name_check CHECK (((char_length(display_name) >= 1) AND (char_length(display_name) <= 255))),
  CONSTRAINT space_library_items_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['ready'::text, 'trash'::text, 'purging'::text, 'deleted'::text]))),
  CONSTRAINT space_library_items_visibility_check CHECK ((visibility = ANY (ARRAY['public'::text, 'private'::text]))),
  CONSTRAINT space_library_items_added_by_user_id_fkey FOREIGN KEY (added_by_user_id) REFERENCES users(id) ON DELETE RESTRICT,
  CONSTRAINT space_library_items_audience_conversation_id_fkey FOREIGN KEY (audience_conversation_id) REFERENCES space_conversations(id) ON DELETE CASCADE,
  CONSTRAINT space_library_items_contributing_user_id_fkey FOREIGN KEY (contributing_user_id) REFERENCES users(id) ON DELETE RESTRICT,
  CONSTRAINT space_library_items_file_id_fkey FOREIGN KEY (file_id) REFERENCES library_files(id) ON DELETE RESTRICT,
  CONSTRAINT space_library_items_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
);
CREATE INDEX space_library_items_discovery_search_idx ON space_library_items USING gin (to_tsvector('simple'::regconfig, ((((display_name || ' '::text) || caption) || ' '::text) || (tags)::text)));
CREATE INDEX space_library_items_favorite_added_idx ON space_library_items USING btree (space_id, added_at DESC, id DESC) WHERE ((lifecycle_state = 'ready'::text) AND favorite);
CREATE INDEX space_library_items_file_idx ON space_library_items USING btree (file_id);
CREATE INDEX space_library_items_hidden_added_idx ON space_library_items USING btree (space_id, added_at DESC, id DESC) WHERE ((lifecycle_state = 'ready'::text) AND hidden);
CREATE INDEX space_library_items_private_owner ON space_library_items USING btree (space_id, added_by_user_id) WHERE (visibility = 'private'::text);
CREATE INDEX space_library_items_space_idx ON space_library_items USING btree (space_id, lifecycle_state, added_at DESC, id);
CREATE INDEX space_library_items_visible_added_idx ON space_library_items USING btree (space_id, hidden, added_at DESC, id DESC) WHERE (lifecycle_state = 'ready'::text);
CREATE TRIGGER library_item_worker_notify AFTER INSERT OR DELETE OR UPDATE OF lifecycle_state, file_id ON space_library_items FOR EACH ROW EXECUTE FUNCTION public.misty_notify_worker_queue('library-ai');
CREATE TRIGGER space_library_items_default_visibility BEFORE INSERT ON space_library_items FOR EACH ROW EXECUTE FUNCTION public.misty_item_default_visibility();
CREATE TRIGGER space_library_items_visibility_guard BEFORE UPDATE OF visibility ON space_library_items FOR EACH ROW EXECUTE FUNCTION public.misty_item_visibility_guard('added_by_user_id');
ALTER TABLE space_library_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_library_items FORCE ROW LEVEL SECURITY;
CREATE POLICY space_library_items_audience_policy ON space_library_items
  USING ((public.misty_rls_is_service() OR (public.misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id) AND ((visibility = 'public'::text) OR (added_by_user_id = public.misty_rls_user_id())))))
  WITH CHECK ((public.misty_rls_is_service() OR (public.misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id) AND ((visibility = 'public'::text) OR (added_by_user_id = public.misty_rls_user_id())))));

CREATE TABLE space_rendition_reservations (
  id text NOT NULL,
  space_id text NOT NULL,
  user_id text NOT NULL,
  source_kind text NOT NULL,
  source_id text NOT NULL,
  reserved_bytes bigint NOT NULL,
  state text NOT NULL,
  expires_at timestamp with time zone NOT NULL,
  created_at timestamp with time zone DEFAULT now() NOT NULL,
  updated_at timestamp with time zone DEFAULT now() NOT NULL,
  CONSTRAINT space_rendition_reservations_pkey PRIMARY KEY (id),
  CONSTRAINT space_rendition_reservations_source_kind_source_id_key UNIQUE (source_kind, source_id),
  CONSTRAINT space_rendition_reservations_reserved_bytes_check CHECK ((reserved_bytes > 0)),
  CONSTRAINT space_rendition_reservations_source_kind_check CHECK ((source_kind = ANY (ARRAY['export'::text, 'preview'::text]))),
  CONSTRAINT space_rendition_reservations_state_check CHECK ((state = ANY (ARRAY['active'::text, 'consumed'::text, 'released'::text]))),
  CONSTRAINT space_rendition_reservations_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE,
  CONSTRAINT space_rendition_reservations_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT
);
CREATE INDEX space_rendition_reservations_active_expiry_idx ON space_rendition_reservations USING btree (expires_at) WHERE (state = 'active'::text);
CREATE INDEX space_rendition_reservations_usage_idx ON space_rendition_reservations USING btree (space_id, state, expires_at);
CREATE TRIGGER rendition_reservation_expiry_insert_notify AFTER INSERT ON space_rendition_reservations FOR EACH ROW EXECUTE FUNCTION public.misty_notify_worker_queue('rendition-reservations');
CREATE TRIGGER rendition_reservation_expiry_update_notify AFTER UPDATE OF state, expires_at ON space_rendition_reservations FOR EACH ROW
  WHEN (((old.state IS DISTINCT FROM new.state) OR (old.expires_at IS DISTINCT FROM new.expires_at)))
  EXECUTE FUNCTION public.misty_notify_worker_queue('rendition-reservations');
ALTER TABLE space_rendition_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_rendition_reservations FORCE ROW LEVEL SECURITY;
CREATE POLICY space_rendition_reservations_policy ON space_rendition_reservations
  USING ((public.misty_rls_is_service() OR ((user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id)) OR public.misty_is_space_owner(space_id)))
  WITH CHECK ((public.misty_rls_is_service() OR ((user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id))));

CREATE TABLE space_library_direct_references (
  id text NOT NULL,
  destination_space_id text NOT NULL,
  grant_id text NOT NULL,
  created_by_user_id text NOT NULL,
  lifecycle_state text DEFAULT 'ready'::text NOT NULL,
  created_at timestamp with time zone DEFAULT now() NOT NULL,
  updated_at timestamp with time zone DEFAULT now() NOT NULL,
  CONSTRAINT space_library_direct_references_pkey PRIMARY KEY (id),
  CONSTRAINT space_library_direct_referenc_destination_space_id_grant_id_key UNIQUE (destination_space_id, grant_id),
  CONSTRAINT space_library_direct_references_lifecycle_state_check CHECK ((lifecycle_state = ANY (ARRAY['ready'::text, 'unavailable'::text, 'deleted'::text]))),
  CONSTRAINT space_library_direct_references_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES users(id) ON DELETE RESTRICT,
  CONSTRAINT space_library_direct_references_destination_space_id_fkey FOREIGN KEY (destination_space_id) REFERENCES spaces(id) ON DELETE CASCADE
);
ALTER TABLE space_library_direct_references ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_library_direct_references FORCE ROW LEVEL SECURITY;
CREATE POLICY direct_references_policy ON space_library_direct_references
  USING ((public.misty_rls_is_service() OR public.misty_is_space_member(destination_space_id)))
  WITH CHECK ((public.misty_rls_is_service() OR public.misty_is_space_member(destination_space_id)));

CREATE TABLE space_library_item_views (
  space_id text NOT NULL,
  space_library_item_id text NOT NULL,
  user_id text NOT NULL,
  view_count bigint DEFAULT 1 NOT NULL,
  first_viewed_at timestamp with time zone DEFAULT now() NOT NULL,
  last_viewed_at timestamp with time zone DEFAULT now() NOT NULL,
  CONSTRAINT space_library_item_views_pkey PRIMARY KEY (space_id, space_library_item_id, user_id),
  CONSTRAINT space_library_item_views_view_count_check CHECK ((view_count > 0)),
  CONSTRAINT space_library_item_views_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE,
  CONSTRAINT space_library_item_views_space_library_item_id_fkey FOREIGN KEY (space_library_item_id) REFERENCES space_library_items(id) ON DELETE CASCADE,
  CONSTRAINT space_library_item_views_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX space_library_item_views_recent_idx ON space_library_item_views USING btree (space_id, user_id, last_viewed_at DESC);
ALTER TABLE space_library_item_views ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_library_item_views FORCE ROW LEVEL SECURITY;
CREATE POLICY space_library_item_views_policy ON space_library_item_views
  USING ((public.misty_rls_is_service() OR ((user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id))))
  WITH CHECK ((public.misty_rls_is_service() OR ((user_id = public.misty_rls_user_id()) AND public.misty_is_space_member(space_id))));

CREATE TABLE space_agent_artifacts (
  item_id text NOT NULL,
  space_id text NOT NULL,
  agent_id text,
  agent_name text NOT NULL,
  owner_user_id text NOT NULL,
  conversation_id text DEFAULT ''::text NOT NULL,
  run_id text DEFAULT ''::text NOT NULL,
  kind text NOT NULL,
  created_at timestamp with time zone DEFAULT now() NOT NULL,
  CONSTRAINT space_agent_artifacts_pkey PRIMARY KEY (item_id),
  CONSTRAINT space_agent_artifacts_agent_name_check CHECK (((char_length(agent_name) >= 1) AND (char_length(agent_name) <= 80))),
  CONSTRAINT space_agent_artifacts_kind_check CHECK ((kind = ANY (ARRAY['download'::text, 'file'::text, 'image'::text, 'code'::text, 'document'::text]))),
  CONSTRAINT space_agent_artifacts_item_id_fkey FOREIGN KEY (item_id) REFERENCES space_library_items(id) ON DELETE CASCADE,
  CONSTRAINT space_agent_artifacts_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES users(id) ON DELETE CASCADE,
  CONSTRAINT space_agent_artifacts_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE
);
CREATE INDEX space_agent_artifacts_agent ON space_agent_artifacts USING btree (agent_id, created_at DESC);
CREATE INDEX space_agent_artifacts_space ON space_agent_artifacts USING btree (space_id, created_at DESC);
ALTER TABLE space_agent_artifacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_agent_artifacts FORCE ROW LEVEL SECURITY;
CREATE POLICY space_agent_artifacts_access ON space_agent_artifacts
  USING ((public.misty_rls_is_service() OR (owner_user_id = public.misty_rls_user_id())))
  WITH CHECK (public.misty_rls_is_service());

CREATE TABLE space_message_library_references (
  message_id text NOT NULL,
  space_id text NOT NULL,
  space_library_item_id text NOT NULL,
  created_by_user_id text NOT NULL,
  created_at timestamp with time zone DEFAULT now() NOT NULL,
  CONSTRAINT space_message_library_references_pkey PRIMARY KEY (message_id, space_library_item_id),
  CONSTRAINT space_message_library_references_created_by_user_id_fkey FOREIGN KEY (created_by_user_id) REFERENCES users(id) ON DELETE RESTRICT,
  CONSTRAINT space_message_library_references_message_id_fkey FOREIGN KEY (message_id) REFERENCES space_messages(id) ON DELETE CASCADE,
  CONSTRAINT space_message_library_references_space_id_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE,
  CONSTRAINT space_message_library_references_space_library_item_id_fkey FOREIGN KEY (space_library_item_id) REFERENCES space_library_items(id) ON DELETE RESTRICT
);
CREATE INDEX space_message_library_references_space_idx ON space_message_library_references USING btree (space_id, message_id);
ALTER TABLE space_message_library_references ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_message_library_references FORCE ROW LEVEL SECURITY;
CREATE POLICY message_library_references_policy ON space_message_library_references
  USING ((public.misty_rls_is_service() OR (public.misty_is_space_member(space_id) AND public.misty_can_read_space_message(message_id))))
  WITH CHECK ((public.misty_rls_is_service() OR (public.misty_is_space_member(space_id) AND public.misty_can_read_space_message(message_id))));

ALTER TABLE space_library_uploads
  ADD COLUMN agent_id text,
  ADD COLUMN agent_conversation_id text,
  ADD COLUMN agent_run_id text,
  ADD COLUMN agent_artifact_kind text,
  ADD COLUMN replace_item_id text,
  ADD COLUMN replace_item_version bigint;
ALTER TABLE space_library_uploads
  ADD CONSTRAINT space_library_uploads_agent_artifact_kind_check CHECK ((agent_artifact_kind = ANY (ARRAY['download'::text, 'file'::text, 'image'::text, 'code'::text, 'document'::text]))),
  ADD CONSTRAINT space_library_uploads_agent_conversation_id_check CHECK ((char_length(agent_conversation_id) <= 200)),
  ADD CONSTRAINT space_library_uploads_agent_run_id_check CHECK ((char_length(agent_run_id) <= 200)),
  ADD CONSTRAINT space_library_uploads_replace_ck CHECK ((((replace_item_id IS NULL) AND (replace_item_version IS NULL)) OR ((purpose = 'library'::text) AND (replace_item_id IS NOT NULL) AND (replace_item_version > 0))));
ALTER TABLE library_derivatives ADD COLUMN space_library_item_id text
  CONSTRAINT library_derivatives_space_library_item_id_fkey REFERENCES space_library_items(id) ON DELETE CASCADE;
ALTER TABLE space_message_attachments ADD COLUMN promoted_item_id text
  CONSTRAINT space_message_attachments_promoted_item_id_fkey REFERENCES space_library_items(id) ON DELETE SET NULL;

-- Everyone may use the Library again, as before.
UPDATE space_roles SET permissions = permissions || ARRAY(
    SELECT p FROM unnest(ARRAY['library.view', 'library.upload', 'library.add', 'library.edit', 'library.download', 'library.import']) WITH ORDINALITY AS t(p, n)
    WHERE p <> ALL (permissions) ORDER BY n),
  updated_at = now()
WHERE is_default;

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_library_items, space_rendition_reservations,
          space_library_direct_references, space_library_item_views, space_agent_artifacts,
          space_message_library_references TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd
