-- +goose Up
-- +goose StatementBegin
CREATE INDEX space_library_items_discovery_search_idx ON space_library_items USING GIN (
    to_tsvector('simple', display_name || ' ' || caption || ' ' || tags::text)
);
CREATE INDEX library_files_discovery_search_idx ON library_files USING GIN (
    to_tsvector('simple', original_filename || ' ' || intrinsic_metadata::text)
);
CREATE INDEX space_library_items_visible_added_idx ON space_library_items(space_id,hidden,added_at DESC,id DESC) WHERE lifecycle_state='ready';
CREATE INDEX space_library_items_favorite_added_idx ON space_library_items(space_id,added_at DESC,id DESC) WHERE lifecycle_state='ready' AND favorite;
CREATE INDEX space_library_items_hidden_added_idx ON space_library_items(space_id,added_at DESC,id DESC) WHERE lifecycle_state='ready' AND hidden;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS space_library_items_hidden_added_idx;
DROP INDEX IF EXISTS space_library_items_favorite_added_idx;
DROP INDEX IF EXISTS space_library_items_visible_added_idx;
DROP INDEX IF EXISTS library_files_discovery_search_idx;
DROP INDEX IF EXISTS space_library_items_discovery_search_idx;
-- +goose StatementEnd
