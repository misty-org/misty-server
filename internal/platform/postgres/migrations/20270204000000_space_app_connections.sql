-- +goose Up
-- +goose StatementBegin
CREATE TABLE space_app_connections (
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 space_id TEXT NOT NULL,
 app_id TEXT NOT NULL,
 connection_id TEXT NOT NULL REFERENCES connected_accounts(id) ON DELETE CASCADE,
 PRIMARY KEY(user_id,space_id,app_id,connection_id),
 FOREIGN KEY(space_id,app_id) REFERENCES space_app_installations(space_id,app_id) ON DELETE CASCADE
);
ALTER TABLE space_app_connections ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_app_connections FORCE ROW LEVEL SECURITY;
CREATE POLICY space_app_connections_owner ON space_app_connections FOR ALL
 USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
 WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());
DO $$ BEGIN IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
 GRANT SELECT,INSERT,UPDATE,DELETE ON space_app_connections TO misty_app;
END IF; END $$;
-- +goose StatementEnd
-- +goose Down
DROP TABLE space_app_connections;
