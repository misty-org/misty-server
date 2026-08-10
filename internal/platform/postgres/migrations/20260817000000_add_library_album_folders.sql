-- +goose Up
-- +goose StatementBegin
CREATE TABLE space_album_folders (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    parent_folder_id TEXT REFERENCES space_album_folders(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    position BIGINT NOT NULL DEFAULT 0,
    created_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (parent_folder_id IS NULL OR parent_folder_id <> id)
);
CREATE UNIQUE INDEX space_album_folders_name_idx ON space_album_folders(space_id,COALESCE(parent_folder_id,''),lower(name));
CREATE INDEX space_album_folders_order_idx ON space_album_folders(space_id,parent_folder_id,position,id);

ALTER TABLE space_albums
    ADD COLUMN folder_id TEXT REFERENCES space_album_folders(id) ON DELETE SET NULL,
    ADD COLUMN position BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN view_mode TEXT NOT NULL DEFAULT 'grid' CHECK (view_mode IN ('grid','list')),
    ADD COLUMN sort_mode TEXT NOT NULL DEFAULT 'custom' CHECK (sort_mode IN ('custom','oldest','newest'));
CREATE INDEX space_albums_folder_order_idx ON space_albums(space_id,folder_id,position,id);

ALTER TABLE space_album_folders ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_album_folders FORCE ROW LEVEL SECURITY;
CREATE POLICY space_album_folders_policy ON space_album_folders FOR ALL
USING (misty_rls_is_service() OR misty_is_space_member(space_id))
WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

DO $grant$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_album_folders TO misty_app;
    END IF;
END $grant$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE space_albums
    DROP COLUMN IF EXISTS sort_mode,
    DROP COLUMN IF EXISTS view_mode,
    DROP COLUMN IF EXISTS position,
    DROP COLUMN IF EXISTS folder_id;
DROP TABLE IF EXISTS space_album_folders;
-- +goose StatementEnd
