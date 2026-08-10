-- +goose Up
-- +goose StatementBegin
CREATE TABLE space_library_asset_stacks (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('live_photo','raw_pair','burst')),
    title TEXT NOT NULL DEFAULT '' CHECK (char_length(title) <= 160),
    cover_item_id TEXT NOT NULL REFERENCES space_library_items(id) ON DELETE RESTRICT,
    motion_item_id TEXT REFERENCES space_library_items(id) ON DELETE SET NULL,
    created_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    lifecycle_state TEXT NOT NULL DEFAULT 'ready' CHECK (lifecycle_state IN ('ready','deleted')),
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX space_library_asset_stacks_space_idx ON space_library_asset_stacks(space_id,kind,created_at DESC) WHERE lifecycle_state='ready';

CREATE TABLE space_library_asset_stack_members (
    stack_id TEXT NOT NULL REFERENCES space_library_asset_stacks(id) ON DELETE CASCADE,
    space_library_item_id TEXT NOT NULL REFERENCES space_library_items(id) ON DELETE RESTRICT,
    role TEXT NOT NULL CHECK (role IN ('still','motion','raw','alternate','burst_frame')),
    position INTEGER NOT NULL DEFAULT 0 CHECK (position >= 0),
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(stack_id,space_library_item_id),
    UNIQUE(stack_id,position)
);
CREATE INDEX space_library_asset_stack_members_item_idx ON space_library_asset_stack_members(space_library_item_id);

ALTER TABLE space_library_asset_stacks ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_library_asset_stacks FORCE ROW LEVEL SECURITY;
CREATE POLICY space_library_asset_stacks_policy ON space_library_asset_stacks FOR ALL
USING (misty_rls_is_service() OR misty_is_space_member(space_id))
WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

ALTER TABLE space_library_asset_stack_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_library_asset_stack_members FORCE ROW LEVEL SECURITY;
CREATE POLICY space_library_asset_stack_members_policy ON space_library_asset_stack_members FOR ALL
USING (misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_library_asset_stacks s WHERE s.id=stack_id AND misty_is_space_member(s.space_id)))
WITH CHECK (misty_rls_is_service() OR EXISTS(SELECT 1 FROM space_library_asset_stacks s WHERE s.id=stack_id AND misty_is_space_member(s.space_id)));

DO $grant$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_library_asset_stacks,space_library_asset_stack_members TO misty_app;
    END IF;
END $grant$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS space_library_asset_stack_members;
DROP TABLE IF EXISTS space_library_asset_stacks;
-- +goose StatementEnd
