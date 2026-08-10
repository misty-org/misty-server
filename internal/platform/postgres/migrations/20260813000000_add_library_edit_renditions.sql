-- +goose Up
-- +goose StatementBegin
ALTER TABLE library_item_versions
    ADD COLUMN rendition_state TEXT NOT NULL DEFAULT 'none'
        CHECK (rendition_state IN ('none','queued','processing','ready','failed')),
    ADD COLUMN rendition_mime_type TEXT NOT NULL DEFAULT '',
    ADD COLUMN rendition_byte_size BIGINT
        CHECK (rendition_byte_size IS NULL OR rendition_byte_size > 0),
    ADD COLUMN rendition_error_code TEXT NOT NULL DEFAULT '',
    ADD COLUMN rendition_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

CREATE TABLE space_rendition_reservations (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('edit','export')),
    source_id TEXT NOT NULL,
    reserved_bytes BIGINT NOT NULL CHECK (reserved_bytes > 0),
    state TEXT NOT NULL CHECK (state IN ('active','consumed','released')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(source_kind,source_id)
);
CREATE INDEX space_rendition_reservations_usage_idx
    ON space_rendition_reservations(space_id,state,expires_at);

ALTER TABLE space_rendition_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_rendition_reservations FORCE ROW LEVEL SECURITY;
CREATE POLICY space_rendition_reservations_policy ON space_rendition_reservations FOR ALL
USING (
    misty_rls_is_service()
    OR user_id=misty_rls_user_id() AND misty_is_space_member(space_id)
    OR misty_is_space_owner(space_id)
)
WITH CHECK (
    misty_rls_is_service()
    OR user_id=misty_rls_user_id() AND misty_is_space_member(space_id)
);

DO $grant$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_rendition_reservations TO misty_app;
    END IF;
END $grant$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS space_rendition_reservations;
ALTER TABLE library_item_versions
    DROP COLUMN IF EXISTS rendition_updated_at,
    DROP COLUMN IF EXISTS rendition_error_code,
    DROP COLUMN IF EXISTS rendition_byte_size,
    DROP COLUMN IF EXISTS rendition_mime_type,
    DROP COLUMN IF EXISTS rendition_state;
-- +goose StatementEnd
