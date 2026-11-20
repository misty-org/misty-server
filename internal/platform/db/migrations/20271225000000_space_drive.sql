-- +goose Up
-- The Space drive: each Space's files as a small file system with one authority, this server.
-- Every change to an item is one transaction that checks the version its sender last saw,
-- bumps the item's version and stamps it with the next number of the Space's change sequence
-- (space_drives.drive_seq), so every computer reads the changes in one order. A trashed item
-- keeps its row; purging leaves a tombstone (deleted_at) that the change feed still carries,
-- until tombstones older than 90 days are pruned and the Space's drive_tombstone_horizon moves
-- up past them. File bytes are the Library's content-addressed blobs (library_blobs), so the
-- same content is stored once per security domain, and they count against the storage limits
-- through one storage contribution per file (source_kind 'drive').
SELECT set_config('app.rls_mode', 'service', true);

-- One row per Space that has used its drive. The row is also the drive's write lock: every
-- change takes it FOR UPDATE first, so changes commit in the order of their sequence numbers.
CREATE TABLE space_drives (
  space_id text PRIMARY KEY REFERENCES spaces(id) ON DELETE CASCADE,
  drive_seq bigint NOT NULL DEFAULT 0 CHECK (drive_seq >= 0),
  drive_tombstone_horizon bigint NOT NULL DEFAULT 0 CHECK (drive_tombstone_horizon >= 0),
  CHECK (drive_tombstone_horizon <= drive_seq)
);

-- Names are NFC, 1-255 bytes, unique among a folder's live children ignoring case. A folder's
-- descendants keep their own trashed_at while it is trashed: they are hidden with it and come
-- back with it. A tombstone has no parent and no blob, so pruning never waits on a child.
CREATE TABLE space_drive_items (
  id text PRIMARY KEY CHECK (id ~ '^drv_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  parent_id text,
  name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 255 AND name NOT IN ('.', '..')
    AND strpos(name, '/') = 0 AND strpos(name, E'\\') = 0),
  kind text NOT NULL CHECK (kind IN ('file', 'folder')),
  sha256 text CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  byte_size bigint NOT NULL DEFAULT 0 CHECK (byte_size >= 0),
  mtime_ms bigint NOT NULL DEFAULT 0,
  -- The blob's object key. A deduplication repair that moves a blob to a new object carries
  -- the item with it, and a blob an item still names can never be removed.
  blob_key text REFERENCES library_blobs(r2_object_key) ON UPDATE CASCADE ON DELETE RESTRICT,
  version bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
  seq bigint NOT NULL CHECK (seq >= 1),
  modified_by text NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  modified_at timestamptz NOT NULL DEFAULT now(),
  created_at timestamptz NOT NULL DEFAULT now(),
  trashed_at timestamptz,
  trashed_by text REFERENCES users(id) ON DELETE RESTRICT,
  trash_hold boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  CONSTRAINT space_drive_items_space_item_key UNIQUE (space_id, id),
  CONSTRAINT space_drive_items_seq_key UNIQUE (space_id, seq),
  -- A parent is a folder of the same Space (the folder check is the server's).
  FOREIGN KEY (space_id, parent_id) REFERENCES space_drive_items(space_id, id),
  CHECK (parent_id IS NULL OR parent_id <> id),
  CHECK ((kind = 'file') = (sha256 IS NOT NULL)),
  CHECK (kind = 'file' OR (byte_size = 0 AND blob_key IS NULL AND mtime_ms = 0)),
  -- A live file has a blob exactly when it has bytes; an empty file has none.
  CHECK (deleted_at IS NOT NULL OR kind = 'folder' OR ((byte_size = 0) = (blob_key IS NULL))),
  CHECK (deleted_at IS NULL OR (blob_key IS NULL AND parent_id IS NULL)),
  CHECK ((trashed_at IS NULL) = (trashed_by IS NULL)),
  CHECK (trashed_at IS NOT NULL OR NOT trash_hold)
);
CREATE UNIQUE INDEX space_drive_items_name_idx ON space_drive_items(space_id, COALESCE(parent_id, ''), lower(name))
  WHERE trashed_at IS NULL AND deleted_at IS NULL;
CREATE INDEX space_drive_items_parent_idx ON space_drive_items(parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX space_drive_items_blob_idx ON space_drive_items(blob_key) WHERE blob_key IS NOT NULL;
CREATE INDEX space_drive_items_trash_idx ON space_drive_items(space_id, trashed_at DESC, id DESC)
  WHERE trashed_at IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX space_drive_items_trash_expiry_idx ON space_drive_items(trashed_at)
  WHERE trashed_at IS NOT NULL AND deleted_at IS NULL AND NOT trash_hold;
CREATE INDEX space_drive_items_tombstone_idx ON space_drive_items(deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX space_drive_items_modified_by_idx ON space_drive_items(modified_by);
CREATE INDEX space_drive_items_trashed_by_idx ON space_drive_items(trashed_by) WHERE trashed_by IS NOT NULL;

-- Who has an item open: a lease of about two minutes that each computer renews while the file
-- is open there. Many rows per item, but at most one editing row per person: their editing
-- place. handover_until keeps a former editing place able to upload what it still has for a
-- moment after Edit here moved editing to another of the person's computers.
CREATE TABLE space_drive_in_use (
  item_id text NOT NULL REFERENCES space_drive_items(id) ON DELETE CASCADE,
  space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_id text NOT NULL CHECK (device_id ~ '^dev_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
  device_name text NOT NULL CHECK (char_length(device_name) BETWEEN 1 AND 64),
  editing boolean NOT NULL DEFAULT false,
  expires_at timestamptz NOT NULL,
  handover_until timestamptz,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (item_id, user_id, device_id)
);
CREATE UNIQUE INDEX space_drive_in_use_editor_idx ON space_drive_in_use(item_id, user_id) WHERE editing;
CREATE INDEX space_drive_in_use_expiry_idx ON space_drive_in_use(expires_at);
CREATE INDEX space_drive_in_use_user_idx ON space_drive_in_use(user_id);

-- A drive file's bytes travel as Library uploads of their own purpose, bound only to the Space.
-- A large file goes up as an S3 multipart upload, whose id and part size the row keeps so the
-- upload resumes after a restart.
ALTER TABLE space_library_uploads DROP CONSTRAINT space_library_uploads_purpose_check;
ALTER TABLE space_library_uploads ADD CONSTRAINT space_library_uploads_purpose_check
  CHECK (purpose IN ('library', 'attachment', 'note_attachment', 'drawing_attachment', 'folder', 'deck_attachment', 'drive'));
ALTER TABLE space_library_uploads ADD COLUMN multipart_upload_id text
  CHECK (multipart_upload_id IS NULL OR char_length(multipart_upload_id) BETWEEN 1 AND 1024);
ALTER TABLE space_library_uploads ADD COLUMN multipart_part_size bigint CHECK (multipart_part_size IS NULL OR multipart_part_size > 0);
ALTER TABLE space_library_uploads ADD CONSTRAINT space_library_uploads_multipart_ck
  CHECK ((multipart_upload_id IS NULL OR purpose = 'drive') AND ((multipart_upload_id IS NULL) = (multipart_part_size IS NULL)));
CREATE INDEX space_library_uploads_drive_idx ON space_library_uploads(space_id, user_id, client_sha256, requested_byte_size)
  WHERE purpose = 'drive';

ALTER TABLE space_storage_contributions DROP CONSTRAINT space_storage_contributions_source_kind_check;
ALTER TABLE space_storage_contributions ADD CONSTRAINT space_storage_contributions_source_kind_check
  CHECK (source_kind IN ('attachment', 'library_item', 'import', 'duplicate', 'edit', 'export',
    'note_asset', 'drawing_asset', 'shared_folder', 'deck_asset', 'item_content', 'drive'));

-- Every member may change the drive (spaces.DefaultEveryonePermissions gains drive.write), and
-- roles that moderate a Space's messages may also empty its trash for good. The owner holds
-- both without a role, and administrator brings both.
UPDATE space_roles SET permissions = array_append(permissions, 'drive.write'), updated_at = now()
WHERE is_default AND NOT ('drive.write' = ANY(permissions));
UPDATE space_roles SET permissions = array_append(permissions, 'drive.purge'), updated_at = now()
WHERE NOT is_default AND 'messages.manage' = ANY(permissions) AND NOT ('drive.purge' = ANY(permissions));

-- Every member reads the drive. The API reaches these tables as the service, which checks
-- membership and permissions itself.
ALTER TABLE space_drives ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_drives FORCE ROW LEVEL SECURITY;
CREATE POLICY space_drives_members ON space_drives
  USING (public.misty_rls_is_service() OR public.misty_is_space_member(space_id))
  WITH CHECK (public.misty_rls_is_service() OR public.misty_is_space_member(space_id));
ALTER TABLE space_drive_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_drive_items FORCE ROW LEVEL SECURITY;
CREATE POLICY space_drive_items_members ON space_drive_items
  USING (public.misty_rls_is_service() OR public.misty_is_space_member(space_id))
  WITH CHECK (public.misty_rls_is_service() OR public.misty_is_space_member(space_id));
ALTER TABLE space_drive_in_use ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_drive_in_use FORCE ROW LEVEL SECURITY;
CREATE POLICY space_drive_in_use_members ON space_drive_in_use
  USING (public.misty_rls_is_service() OR public.misty_is_space_member(space_id))
  WITH CHECK (public.misty_rls_is_service() OR public.misty_is_space_member(space_id));

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_drives, space_drive_items, space_drive_in_use TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);

UPDATE space_roles SET permissions = array_remove(array_remove(permissions, 'drive.write'), 'drive.purge'), updated_at = now()
WHERE 'drive.write' = ANY(permissions) OR 'drive.purge' = ANY(permissions);

DELETE FROM space_storage_contributions WHERE source_kind = 'drive';
ALTER TABLE space_storage_contributions DROP CONSTRAINT space_storage_contributions_source_kind_check;
ALTER TABLE space_storage_contributions ADD CONSTRAINT space_storage_contributions_source_kind_check
  CHECK (source_kind IN ('attachment', 'library_item', 'import', 'duplicate', 'edit', 'export',
    'note_asset', 'drawing_asset', 'shared_folder', 'deck_asset', 'item_content'));

DELETE FROM space_upload_reservations WHERE upload_id IN (SELECT id FROM space_library_uploads WHERE purpose = 'drive');
DELETE FROM space_library_uploads WHERE purpose = 'drive';
DROP INDEX IF EXISTS space_library_uploads_drive_idx;
ALTER TABLE space_library_uploads DROP CONSTRAINT IF EXISTS space_library_uploads_multipart_ck;
ALTER TABLE space_library_uploads DROP COLUMN IF EXISTS multipart_part_size;
ALTER TABLE space_library_uploads DROP COLUMN IF EXISTS multipart_upload_id;
ALTER TABLE space_library_uploads DROP CONSTRAINT space_library_uploads_purpose_check;
ALTER TABLE space_library_uploads ADD CONSTRAINT space_library_uploads_purpose_check
  CHECK (purpose IN ('library', 'attachment', 'note_attachment', 'drawing_attachment', 'folder', 'deck_attachment'));

DROP TABLE IF EXISTS space_drive_in_use;
DROP TABLE IF EXISTS space_drive_items;
DROP TABLE IF EXISTS space_drives;
