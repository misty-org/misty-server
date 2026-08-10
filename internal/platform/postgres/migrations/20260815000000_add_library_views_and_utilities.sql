-- +goose Up
-- +goose StatementBegin
CREATE TABLE space_library_item_views (
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    space_library_item_id TEXT NOT NULL REFERENCES space_library_items(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    view_count BIGINT NOT NULL DEFAULT 1 CHECK (view_count>0),
    first_viewed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_viewed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(space_id,space_library_item_id,user_id)
);
CREATE INDEX space_library_item_views_recent_idx ON space_library_item_views(space_id,user_id,last_viewed_at DESC);

ALTER TABLE space_library_item_views ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_library_item_views FORCE ROW LEVEL SECURITY;
CREATE POLICY space_library_item_views_policy ON space_library_item_views FOR ALL
USING (misty_rls_is_service() OR (user_id=misty_rls_user_id() AND misty_is_space_member(space_id)))
WITH CHECK (misty_rls_is_service() OR (user_id=misty_rls_user_id() AND misty_is_space_member(space_id)));

DO $grant$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_library_item_views TO misty_app;
    END IF;
END $grant$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS space_library_item_views;
-- +goose StatementEnd
