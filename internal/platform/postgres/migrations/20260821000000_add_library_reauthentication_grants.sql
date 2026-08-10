-- +goose Up
-- +goose StatementBegin
CREATE TABLE library_reauthentication_grants (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    scope TEXT NOT NULL CHECK (scope IN ('hidden','recently_deleted','bulk_export')),
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX library_reauthentication_grants_lookup_idx ON library_reauthentication_grants(user_id,space_id,scope,expires_at DESC);
ALTER TABLE library_reauthentication_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE library_reauthentication_grants FORCE ROW LEVEL SECURITY;
CREATE POLICY library_reauthentication_grants_policy ON library_reauthentication_grants FOR ALL
USING (misty_rls_is_service() OR (user_id=misty_rls_user_id() AND misty_is_space_member(space_id)))
WITH CHECK (misty_rls_is_service() OR (user_id=misty_rls_user_id() AND misty_is_space_member(space_id)));
DO $grant$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON library_reauthentication_grants TO misty_app;
    END IF;
END $grant$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS library_reauthentication_grants;
-- +goose StatementEnd
