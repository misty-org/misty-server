-- +goose Up
-- +goose StatementBegin
CREATE TABLE space_pinned_collections (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('system','album','group','person','memory','trip','map')),
    target_id TEXT NOT NULL CHECK (char_length(target_id) BETWEEN 1 AND 255),
    position INTEGER NOT NULL CHECK (position BETWEEN 0 AND 99),
    pinned_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(space_id,target_kind,target_id),
    UNIQUE(space_id,position)
);
CREATE INDEX space_pinned_collections_order_idx ON space_pinned_collections(space_id,position);

ALTER TABLE space_pinned_collections ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_pinned_collections FORCE ROW LEVEL SECURITY;
CREATE POLICY space_pinned_collections_policy ON space_pinned_collections FOR ALL
USING (misty_rls_is_service() OR misty_is_space_member(space_id))
WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

DO $grant$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_pinned_collections TO misty_app;
    END IF;
END $grant$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS space_pinned_collections;
-- +goose StatementEnd
