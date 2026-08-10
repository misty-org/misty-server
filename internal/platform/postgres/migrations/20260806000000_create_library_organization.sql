-- +goose Up
-- +goose StatementBegin
CREATE TABLE space_message_library_references (
    message_id TEXT NOT NULL REFERENCES space_messages(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    space_library_item_id TEXT NOT NULL REFERENCES space_library_items(id) ON DELETE RESTRICT,
    created_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(message_id,space_library_item_id)
);
CREATE INDEX space_message_library_references_space_idx ON space_message_library_references(space_id,message_id);

CREATE TABLE space_albums (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 2000),
    cover_item_id TEXT REFERENCES space_library_items(id) ON DELETE SET NULL,
    created_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(space_id,name)
);
CREATE TABLE space_album_items (
    album_id TEXT NOT NULL REFERENCES space_albums(id) ON DELETE CASCADE,
    space_library_item_id TEXT NOT NULL REFERENCES space_library_items(id) ON DELETE CASCADE,
    added_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    position BIGINT NOT NULL DEFAULT 0,
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(album_id,space_library_item_id)
);

CREATE TABLE space_library_groups (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    rules JSONB NOT NULL DEFAULT '{"all":[]}'::jsonb,
    created_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (octet_length(rules::text) <= 16384),
    UNIQUE(space_id,name)
);

CREATE TABLE space_people (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL DEFAULT '' CHECK (char_length(name) <= 120),
    cover_item_id TEXT REFERENCES space_library_items(id) ON DELETE SET NULL,
    lifecycle_state TEXT NOT NULL DEFAULT 'active' CHECK (lifecycle_state IN ('active','merged','deleted')),
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE space_person_observations (
    person_id TEXT NOT NULL REFERENCES space_people(id) ON DELETE CASCADE,
    space_library_item_id TEXT NOT NULL REFERENCES space_library_items(id) ON DELETE CASCADE,
    derivative_id TEXT REFERENCES library_derivatives(id) ON DELETE CASCADE,
    confidence REAL NOT NULL CHECK (confidence BETWEEN 0 AND 1),
    bounds JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(person_id,space_library_item_id,derivative_id)
);

ALTER TABLE space_message_library_references ENABLE ROW LEVEL SECURITY; ALTER TABLE space_message_library_references FORCE ROW LEVEL SECURITY;
CREATE POLICY message_library_references_policy ON space_message_library_references FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
ALTER TABLE space_albums ENABLE ROW LEVEL SECURITY; ALTER TABLE space_albums FORCE ROW LEVEL SECURITY;
CREATE POLICY space_albums_policy ON space_albums FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
ALTER TABLE space_album_items ENABLE ROW LEVEL SECURITY; ALTER TABLE space_album_items FORCE ROW LEVEL SECURITY;
CREATE POLICY space_album_items_policy ON space_album_items FOR ALL USING (misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_albums a WHERE a.id=album_id AND misty_is_space_member(a.space_id))) WITH CHECK (misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_albums a WHERE a.id=album_id AND misty_is_space_member(a.space_id)));
ALTER TABLE space_library_groups ENABLE ROW LEVEL SECURITY; ALTER TABLE space_library_groups FORCE ROW LEVEL SECURITY;
CREATE POLICY space_library_groups_policy ON space_library_groups FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
ALTER TABLE space_people ENABLE ROW LEVEL SECURITY; ALTER TABLE space_people FORCE ROW LEVEL SECURITY;
CREATE POLICY space_people_policy ON space_people FOR ALL USING (misty_rls_is_service() OR misty_is_space_member(space_id)) WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
ALTER TABLE space_person_observations ENABLE ROW LEVEL SECURITY; ALTER TABLE space_person_observations FORCE ROW LEVEL SECURITY;
CREATE POLICY space_person_observations_policy ON space_person_observations FOR ALL USING (misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_people p WHERE p.id=person_id AND misty_is_space_member(p.space_id))) WITH CHECK (misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_people p WHERE p.id=person_id AND misty_is_space_member(p.space_id)));

DO $grant$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_message_library_references,space_albums,space_album_items,space_library_groups,space_people,space_person_observations TO misty_app;
    END IF;
END $grant$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS space_person_observations,space_people,space_library_groups,space_album_items,space_albums,space_message_library_references;
-- +goose StatementEnd
