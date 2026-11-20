-- +goose Up
-- The drive's simpler model: the last save wins and nothing is lost. A save that replaces
-- content its sender never saw keeps the replaced content as a trashed copy in the same folder,
-- which names the item it was replaced in (replaced_of) and shares its blob. Whether the sender
-- saw the content is read from content_version, the version at which the content last changed,
-- so a rename, move or trash since never counts as content it missed. A person may edit a
-- file on more than one of their computers, so there is no editing place any more: the one
-- editing mark per person and the handover window after Edit here go.
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE space_drive_items ADD COLUMN replaced_of text REFERENCES space_drive_items(id) ON DELETE SET NULL;
ALTER TABLE space_drive_items ADD CONSTRAINT space_drive_items_replaced_of_ck CHECK (replaced_of IS NULL OR replaced_of <> id);
CREATE INDEX space_drive_items_replaced_of_idx ON space_drive_items(replaced_of) WHERE replaced_of IS NOT NULL;
ALTER TABLE space_drive_items ADD COLUMN content_version bigint;
UPDATE space_drive_items SET content_version = version;
ALTER TABLE space_drive_items ALTER COLUMN content_version SET DEFAULT 1;
ALTER TABLE space_drive_items ALTER COLUMN content_version SET NOT NULL;
ALTER TABLE space_drive_items ADD CONSTRAINT space_drive_items_content_version_ck
  CHECK (content_version >= 1 AND content_version <= version);

DROP INDEX IF EXISTS space_drive_in_use_editor_idx;
ALTER TABLE space_drive_in_use DROP COLUMN IF EXISTS handover_until;

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE space_drive_in_use ADD COLUMN IF NOT EXISTS handover_until timestamptz;
-- One editing mark per person again: keep each person's newest.
UPDATE space_drive_in_use m SET editing = false
WHERE m.editing AND EXISTS (
  SELECT 1 FROM space_drive_in_use o
  WHERE o.item_id = m.item_id AND o.user_id = m.user_id AND o.editing
    AND (o.updated_at, o.device_id) > (m.updated_at, m.device_id));
CREATE UNIQUE INDEX IF NOT EXISTS space_drive_in_use_editor_idx ON space_drive_in_use(item_id, user_id) WHERE editing;

ALTER TABLE space_drive_items DROP CONSTRAINT IF EXISTS space_drive_items_content_version_ck;
ALTER TABLE space_drive_items DROP COLUMN IF EXISTS content_version;
DROP INDEX IF EXISTS space_drive_items_replaced_of_idx;
ALTER TABLE space_drive_items DROP CONSTRAINT IF EXISTS space_drive_items_replaced_of_ck;
ALTER TABLE space_drive_items DROP COLUMN IF EXISTS replaced_of;
