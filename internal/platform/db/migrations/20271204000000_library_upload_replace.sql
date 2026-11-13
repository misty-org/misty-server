-- +goose Up
-- An upload can replace an existing Library item's content (the photo editor's Save). It names
-- the item and the version the editor started from; finishing it checks that version again.
ALTER TABLE space_library_uploads
    ADD COLUMN replace_item_id text,
    ADD COLUMN replace_item_version bigint,
    ADD CONSTRAINT space_library_uploads_replace_ck CHECK (
        (replace_item_id IS NULL AND replace_item_version IS NULL)
        OR (purpose = 'library' AND replace_item_id IS NOT NULL AND replace_item_version > 0)
    );

-- +goose Down
ALTER TABLE space_library_uploads
    DROP CONSTRAINT space_library_uploads_replace_ck,
    DROP COLUMN replace_item_version,
    DROP COLUMN replace_item_id;
