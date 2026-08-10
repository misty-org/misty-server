-- +goose Up
-- +goose StatementBegin
CREATE TABLE space_memory_preferences (
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    memory_id TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '' CHECK (char_length(title) <= 160),
    cover_item_id TEXT REFERENCES space_library_items(id) ON DELETE SET NULL,
    music_item_id TEXT REFERENCES space_library_items(id) ON DELETE SET NULL,
    playback_seconds REAL NOT NULL DEFAULT 4.5 CHECK (playback_seconds BETWEEN 1 AND 15),
    updated_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(space_id,memory_id)
);
ALTER TABLE space_memory_preferences ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_memory_preferences FORCE ROW LEVEL SECURITY;
CREATE POLICY space_memory_preferences_policy ON space_memory_preferences FOR ALL
USING (misty_rls_is_service() OR misty_is_space_member(space_id))
WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
DO $grant$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_memory_preferences TO misty_app;
    END IF;
END $grant$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS space_memory_preferences;
-- +goose StatementEnd
