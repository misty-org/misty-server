-- +goose Up
-- Shared folders fold into the Space drive. Each live shared folder becomes a folder at the
-- root of its Space's drive, named after it (" (2)", " (3)"... on a name clash), holding its
-- files as drive items and the folders their paths name. The files keep their blobs, so no
-- byte is copied, and their storage moves from the folder's one contribution (source_kind
-- 'shared_folder') to one per file (source_kind 'drive'), still counted against whoever
-- uploaded the folder last. Pending folder uploads are given up: their reservations free and
-- the Library's orphan sweep collects whatever they sent. Then the shared-folder tables go.
SELECT set_config('app.rls_mode', 'service', true);

-- A path segment or folder name as a drive name: NFC, no slashes or control characters,
-- never "." or "..", and at most 255 bytes.
-- +goose StatementBegin
CREATE FUNCTION misty_drive_migrate_safe_name(raw text) RETURNS text LANGUAGE plpgsql AS $$
DECLARE
  result text := normalize(raw, NFC);
BEGIN
  result := translate(result, E'/\\', '__');
  result := regexp_replace(result, '[[:cntrl:]]', '_', 'g');
  IF result = '' OR result = '.' OR result = '..' THEN
    result := '_' || result;
  END IF;
  WHILE octet_length(result) > 255 LOOP
    result := left(result, char_length(result) - 1);
  END LOOP;
  RETURN result;
END $$;
-- +goose StatementEnd

-- The first of name, "name (2)", "name (3)"... that no live sibling has, ignoring case. A
-- file keeps its extension after the number.
-- +goose StatementBegin
CREATE FUNCTION misty_drive_migrate_unique_name(space text, parent text, wanted text, item_kind text)
RETURNS text LANGUAGE plpgsql AS $$
DECLARE
  base text := wanted;
  ext text := '';
  dot integer;
  candidate text := wanted;
  n integer := 2;
BEGIN
  IF item_kind = 'file' THEN
    dot := char_length(wanted) - strpos(reverse(wanted), '.') + 1;
    IF strpos(wanted, '.') > 0 AND dot > 1 THEN
      base := left(wanted, dot - 1);
      ext := substr(wanted, dot);
    END IF;
  END IF;
  WHILE EXISTS (
    SELECT 1 FROM space_drive_items
    WHERE space_id = space AND COALESCE(parent_id, '') = COALESCE(parent, '')
      AND lower(name) = lower(candidate) AND trashed_at IS NULL AND deleted_at IS NULL
  ) LOOP
    candidate := base || ' (' || n || ')' || ext;
    WHILE octet_length(candidate) > 255 AND char_length(base) > 1 LOOP
      base := left(base, char_length(base) - 1);
      candidate := base || ' (' || n || ')' || ext;
    END LOOP;
    n := n + 1;
  END LOOP;
  RETURN candidate;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION misty_drive_migrate_next_seq(space text) RETURNS bigint LANGUAGE plpgsql AS $$
DECLARE
  next_seq bigint;
BEGIN
  INSERT INTO space_drives(space_id) VALUES (space) ON CONFLICT DO NOTHING;
  UPDATE space_drives SET drive_seq = drive_seq + 1 WHERE space_id = space RETURNING drive_seq INTO next_seq;
  RETURN next_seq;
END $$;
-- +goose StatementEnd

-- The folder made for each path segment, by its parent and the segment ignoring case. A folder
-- can't be found again by its name, which differs from the segment when a file took the name
-- first ("Docs (2)").
CREATE TEMP TABLE misty_drive_migrate_folders (
  folder_parent text NOT NULL,
  folder_key text NOT NULL,
  folder_id text NOT NULL,
  PRIMARY KEY (folder_parent, folder_key)
);

-- +goose StatementBegin
DO $$
DECLARE
  shared record;
  entry record;
  segments text[];
  segment text;
  parent text;
  child text;
  item_id text;
  stamp bigint;
  i integer;
BEGIN
  FOR shared IN
    SELECT * FROM space_shared_folders WHERE deleted_at IS NULL ORDER BY space_id, uploaded_at, id
  LOOP
    stamp := floor(extract(epoch FROM shared.uploaded_at) * 1000)::bigint;
    parent := 'drv_' || gen_random_uuid();
    INSERT INTO space_drive_items(id, space_id, parent_id, name, kind, seq, modified_by, modified_at, created_at)
    VALUES (parent, shared.space_id, NULL,
      misty_drive_migrate_unique_name(shared.space_id, NULL, misty_drive_migrate_safe_name(shared.name), 'folder'),
      'folder', misty_drive_migrate_next_seq(shared.space_id), shared.uploaded_by, shared.uploaded_at, shared.created_at);
    item_id := parent;

    FOR entry IN
      SELECT * FROM space_shared_folder_files WHERE folder_id = shared.id ORDER BY path
    LOOP
      segments := string_to_array(entry.path, '/');
      parent := item_id;
      -- Every folder the path names, made once and shared by the files under it.
      FOR i IN 1 .. array_length(segments, 1) - 1 LOOP
        segment := misty_drive_migrate_safe_name(segments[i]);
        child := NULL;
        SELECT folder_id INTO child FROM misty_drive_migrate_folders
        WHERE folder_parent = parent AND folder_key = lower(segment);
        IF child IS NULL THEN
          child := 'drv_' || gen_random_uuid();
          INSERT INTO space_drive_items(id, space_id, parent_id, name, kind, seq, modified_by, modified_at, created_at)
          VALUES (child, shared.space_id, parent,
            misty_drive_migrate_unique_name(shared.space_id, parent, segment, 'folder'),
            'folder', misty_drive_migrate_next_seq(shared.space_id), shared.uploaded_by, shared.uploaded_at, shared.created_at);
          INSERT INTO misty_drive_migrate_folders(folder_parent, folder_key, folder_id) VALUES (parent, lower(segment), child);
        END IF;
        parent := child;
      END LOOP;

      child := 'drv_' || gen_random_uuid();
      INSERT INTO space_drive_items(id, space_id, parent_id, name, kind, sha256, byte_size, mtime_ms, blob_key,
        seq, modified_by, modified_at, created_at)
      VALUES (child, shared.space_id, parent,
        misty_drive_migrate_unique_name(shared.space_id, parent,
          misty_drive_migrate_safe_name(segments[array_length(segments, 1)]), 'file'),
        'file', entry.sha256, entry.byte_size, stamp, entry.blob_key,
        misty_drive_migrate_next_seq(shared.space_id), shared.uploaded_by, shared.uploaded_at, shared.uploaded_at);
      IF entry.byte_size > 0 THEN
        INSERT INTO space_storage_contributions(id, space_id, user_id, file_id, source_kind, source_id, logical_bytes, state)
        VALUES ('contribution_' || gen_random_uuid(), shared.space_id, shared.uploaded_by, NULL, 'drive', child, entry.byte_size, 'active');
      END IF;
    END LOOP;
  END LOOP;
END $$;
-- +goose StatementEnd

DROP TABLE misty_drive_migrate_folders;
DROP FUNCTION misty_drive_migrate_next_seq(text);
DROP FUNCTION misty_drive_migrate_unique_name(text, text, text, text);
DROP FUNCTION misty_drive_migrate_safe_name(text);

-- The folders' own contributions are replaced by the files' above.
DELETE FROM space_storage_contributions WHERE source_kind = 'shared_folder';

-- Pending folder uploads are given up; every folder upload row becomes a drive upload, so the
-- rows that first wrote today's blobs stay coherent with the purpose list below.
UPDATE space_upload_reservations SET state = 'released', updated_at = now()
WHERE state = 'active' AND upload_id IN (SELECT id FROM space_library_uploads WHERE purpose = 'folder');
UPDATE space_library_uploads SET state = 'rejected', error_code = 'superseded', version = version + 1, updated_at = now()
WHERE purpose = 'folder' AND state IN ('initiated', 'uploading', 'uploaded_unverified');
DROP INDEX IF EXISTS space_library_uploads_shared_folder_idx;
ALTER TABLE space_library_uploads DROP CONSTRAINT IF EXISTS space_library_uploads_folder_purpose_ck;
ALTER TABLE space_library_uploads DROP COLUMN shared_folder_id;
UPDATE space_library_uploads SET purpose = 'drive' WHERE purpose = 'folder';
ALTER TABLE space_library_uploads DROP CONSTRAINT space_library_uploads_purpose_check;
ALTER TABLE space_library_uploads ADD CONSTRAINT space_library_uploads_purpose_check
  CHECK (purpose IN ('library', 'attachment', 'note_attachment', 'drawing_attachment', 'deck_attachment', 'drive'));

ALTER TABLE space_storage_contributions DROP CONSTRAINT space_storage_contributions_source_kind_check;
ALTER TABLE space_storage_contributions ADD CONSTRAINT space_storage_contributions_source_kind_check
  CHECK (source_kind IN ('attachment', 'library_item', 'import', 'duplicate', 'edit', 'export',
    'note_asset', 'drawing_asset', 'deck_asset', 'item_content', 'drive'));

-- Each Space's counters, taken again from its ledgers.
UPDATE space_storage_usage u SET
  used_bytes = COALESCE((SELECT sum(logical_bytes) FROM space_storage_contributions c
    WHERE c.space_id = u.space_id AND c.state IN ('active', 'recovery')), 0),
  reserved_bytes = COALESCE((SELECT sum(reserved_bytes) FROM space_upload_reservations r
    WHERE r.space_id = u.space_id AND r.state = 'active'), 0)
    + COALESCE((SELECT sum(reserved_bytes) FROM space_rendition_reservations r
    WHERE r.space_id = u.space_id AND r.state = 'active'), 0),
  version = u.version + 1, updated_at = now();

DROP TABLE space_shared_folder_files;
DROP TABLE space_shared_folders;

-- +goose Down
-- The shared-folder tables come back empty: the drive keeps the folders this migration made.
SELECT set_config('app.rls_mode', 'service', true);

CREATE TABLE space_shared_folders (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
  uploaded_by text NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  uploaded_at timestamptz NOT NULL DEFAULT now(),
  byte_size bigint NOT NULL DEFAULT 0 CHECK (byte_size >= 0),
  file_count integer NOT NULL DEFAULT 0 CHECK (file_count BETWEEN 0 AND 50000),
  created_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz
);
CREATE INDEX space_shared_folders_space_idx ON space_shared_folders(space_id, name) WHERE deleted_at IS NULL;
CREATE INDEX space_shared_folders_uploaded_by_idx ON space_shared_folders(uploaded_by);

CREATE TABLE space_shared_folder_files (
  folder_id uuid NOT NULL REFERENCES space_shared_folders(id) ON DELETE CASCADE,
  path text NOT NULL CHECK (char_length(path) BETWEEN 1 AND 1024),
  sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  byte_size bigint NOT NULL CHECK (byte_size >= 0),
  blob_key text REFERENCES library_blobs(r2_object_key) ON UPDATE CASCADE ON DELETE RESTRICT,
  UNIQUE (folder_id, path),
  CHECK ((byte_size = 0) = (blob_key IS NULL))
);
CREATE INDEX space_shared_folder_files_blob_idx ON space_shared_folder_files(blob_key) WHERE blob_key IS NOT NULL;

ALTER TABLE space_shared_folders ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_shared_folders FORCE ROW LEVEL SECURITY;
CREATE POLICY space_shared_folders_members ON space_shared_folders
  USING (public.misty_rls_is_service() OR public.misty_is_space_member(space_id))
  WITH CHECK (public.misty_rls_is_service() OR public.misty_is_space_member(space_id));
ALTER TABLE space_shared_folder_files ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_shared_folder_files FORCE ROW LEVEL SECURITY;
CREATE POLICY space_shared_folder_files_members ON space_shared_folder_files
  USING (public.misty_rls_is_service() OR EXISTS (
    SELECT 1 FROM space_shared_folders f WHERE f.id = space_shared_folder_files.folder_id))
  WITH CHECK (public.misty_rls_is_service() OR EXISTS (
    SELECT 1 FROM space_shared_folders f WHERE f.id = space_shared_folder_files.folder_id));

ALTER TABLE space_storage_contributions DROP CONSTRAINT space_storage_contributions_source_kind_check;
ALTER TABLE space_storage_contributions ADD CONSTRAINT space_storage_contributions_source_kind_check
  CHECK (source_kind IN ('attachment', 'library_item', 'import', 'duplicate', 'edit', 'export',
    'note_asset', 'drawing_asset', 'shared_folder', 'deck_asset', 'item_content', 'drive'));

ALTER TABLE space_library_uploads DROP CONSTRAINT space_library_uploads_purpose_check;
ALTER TABLE space_library_uploads ADD CONSTRAINT space_library_uploads_purpose_check
  CHECK (purpose IN ('library', 'attachment', 'note_attachment', 'drawing_attachment', 'folder', 'deck_attachment', 'drive'));
ALTER TABLE space_library_uploads ADD COLUMN shared_folder_id uuid
  REFERENCES space_shared_folders(id) ON DELETE CASCADE;
ALTER TABLE space_library_uploads ADD CONSTRAINT space_library_uploads_folder_purpose_ck
  CHECK ((purpose = 'folder') = (shared_folder_id IS NOT NULL));
CREATE INDEX space_library_uploads_shared_folder_idx ON space_library_uploads(shared_folder_id, user_id)
  WHERE shared_folder_id IS NOT NULL;

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_shared_folders, space_shared_folder_files TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd
