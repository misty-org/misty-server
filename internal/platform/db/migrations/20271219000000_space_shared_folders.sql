-- +goose Up
-- Shared Space folders: a folder a member uploads to a Space, which the other members download.
-- The folder's files live in the Library's content-addressed blobs (library_blobs), so a file
-- already in the Space's security domain is never stored twice. A folder is one manifest of
-- relative paths; each upload replaces it whole and the last commit wins. Its bytes count
-- against the storage limits through one storage contribution, attributed to the member who
-- uploaded it last.
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

-- An empty file has no blob: its size is 0 and its digest is the empty digest.
CREATE TABLE space_shared_folder_files (
  folder_id uuid NOT NULL REFERENCES space_shared_folders(id) ON DELETE CASCADE,
  path text NOT NULL CHECK (char_length(path) BETWEEN 1 AND 1024),
  sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  byte_size bigint NOT NULL CHECK (byte_size >= 0),
  -- The blob's object key. A deduplication repair that moves a blob to a new object carries
  -- the folder with it, and a blob a folder still names can never be removed.
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
-- A file is visible exactly when its folder is, as a note's assets follow their note.
ALTER TABLE space_shared_folder_files ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_shared_folder_files FORCE ROW LEVEL SECURITY;
CREATE POLICY space_shared_folder_files_members ON space_shared_folder_files
  USING (public.misty_rls_is_service() OR EXISTS (
    SELECT 1 FROM space_shared_folders f WHERE f.id = space_shared_folder_files.folder_id))
  WITH CHECK (public.misty_rls_is_service() OR EXISTS (
    SELECT 1 FROM space_shared_folders f WHERE f.id = space_shared_folder_files.folder_id));

-- A folder's file bytes travel as Library uploads of their own purpose, bound to the folder.
ALTER TABLE space_library_uploads DROP CONSTRAINT space_library_uploads_purpose_check;
ALTER TABLE space_library_uploads ADD CONSTRAINT space_library_uploads_purpose_check
  CHECK (purpose IN ('library', 'attachment', 'note_attachment', 'drawing_attachment', 'folder'));
ALTER TABLE space_library_uploads ADD COLUMN shared_folder_id uuid
  REFERENCES space_shared_folders(id) ON DELETE CASCADE;
ALTER TABLE space_library_uploads ADD CONSTRAINT space_library_uploads_folder_purpose_ck
  CHECK ((purpose = 'folder') = (shared_folder_id IS NOT NULL));
CREATE INDEX space_library_uploads_shared_folder_idx ON space_library_uploads(shared_folder_id, user_id)
  WHERE shared_folder_id IS NOT NULL;

ALTER TABLE space_storage_contributions DROP CONSTRAINT space_storage_contributions_source_kind_check;
ALTER TABLE space_storage_contributions ADD CONSTRAINT space_storage_contributions_source_kind_check
  CHECK (source_kind IN ('attachment', 'library_item', 'import', 'duplicate', 'edit', 'export',
    'note_asset', 'drawing_asset', 'shared_folder'));

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_shared_folders, space_shared_folder_files TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);

DELETE FROM space_storage_contributions WHERE source_kind = 'shared_folder';
ALTER TABLE space_storage_contributions DROP CONSTRAINT space_storage_contributions_source_kind_check;
ALTER TABLE space_storage_contributions ADD CONSTRAINT space_storage_contributions_source_kind_check
  CHECK (source_kind IN ('attachment', 'library_item', 'import', 'duplicate', 'edit', 'export',
    'note_asset', 'drawing_asset'));
DELETE FROM space_library_uploads WHERE purpose = 'folder';
DROP INDEX IF EXISTS space_library_uploads_shared_folder_idx;
ALTER TABLE space_library_uploads DROP CONSTRAINT IF EXISTS space_library_uploads_folder_purpose_ck;
ALTER TABLE space_library_uploads DROP COLUMN IF EXISTS shared_folder_id;
ALTER TABLE space_library_uploads DROP CONSTRAINT space_library_uploads_purpose_check;
ALTER TABLE space_library_uploads ADD CONSTRAINT space_library_uploads_purpose_check
  CHECK (purpose IN ('library', 'attachment', 'note_attachment', 'drawing_attachment'));
DROP TABLE IF EXISTS space_shared_folder_files;
DROP TABLE IF EXISTS space_shared_folders;
