-- +goose Up
-- +goose StatementBegin
CREATE TABLE space_device_presence (
 owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 device_id TEXT NOT NULL REFERENCES trusted_devices(id) ON DELETE CASCADE,
 space_id TEXT NOT NULL,
 app_id TEXT NOT NULL CHECK(app_id='files'),
 installed_version TEXT NOT NULL,
 authority_generation BIGINT NOT NULL CHECK(authority_generation>0),
 endpoint_id TEXT NOT NULL,
 addressing JSONB NOT NULL CHECK(jsonb_typeof(addressing)='object'),
 protocol_version TEXT NOT NULL CHECK(protocol_version='misty-device/2'),
 connection_hint TEXT NOT NULL CHECK(connection_hint IN ('unknown','direct','relay')),
 last_heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(owner_user_id,device_id,space_id,app_id),
 UNIQUE(owner_user_id,device_id,endpoint_id),
 FOREIGN KEY(space_id,app_id) REFERENCES space_app_installations(space_id,app_id) ON DELETE CASCADE
);
ALTER TABLE space_device_presence ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_device_presence FORCE ROW LEVEL SECURITY;
CREATE POLICY space_device_presence_service ON space_device_presence FOR ALL
 USING(misty_rls_is_service()) WITH CHECK(misty_rls_is_service());
CREATE POLICY space_device_presence_owner_read ON space_device_presence FOR SELECT
 USING(owner_user_id=misty_rls_user_id() AND EXISTS(
 SELECT 1 FROM space_members m JOIN space_app_installations a ON a.space_id=m.space_id
 WHERE m.user_id=misty_rls_user_id() AND m.space_id=space_device_presence.space_id
 AND a.app_id=space_device_presence.app_id AND a.state='installed'
 AND a.authority_generation=space_device_presence.authority_generation
 AND a.installed_version=space_device_presence.installed_version));
DO $$ BEGIN IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
 GRANT SELECT,INSERT,UPDATE,DELETE ON space_device_presence TO misty_app;
END IF; END $$;
-- +goose StatementEnd
-- +goose Down
DROP TABLE space_device_presence;
