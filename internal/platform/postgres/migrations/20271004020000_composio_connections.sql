-- +goose Up
CREATE TABLE composio_connections (
 id text PRIMARY KEY, owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 instance_id text NOT NULL, external_id text NOT NULL, name text NOT NULL,
 status text NOT NULL CHECK(status IN ('pending','active','needs_attention','revoked')),
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(id,owner_user_id), UNIQUE(instance_id,external_id)
);
CREATE TABLE composio_agent_bindings (
 connection_id text NOT NULL, owner_user_id text NOT NULL, agent_id text NOT NULL,
 calendar_id text NOT NULL CHECK(length(calendar_id) BETWEEN 1 AND 320),
 enabled boolean NOT NULL DEFAULT false, updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(connection_id,agent_id), FOREIGN KEY(connection_id,owner_user_id) REFERENCES composio_connections(id,owner_user_id) ON DELETE CASCADE
);
ALTER TABLE composio_connections ENABLE ROW LEVEL SECURITY;
ALTER TABLE composio_connections FORCE ROW LEVEL SECURITY;
CREATE POLICY composio_owner ON composio_connections USING(misty_rls_is_service() OR owner_user_id=misty_rls_user_id()) WITH CHECK(misty_rls_is_service() OR owner_user_id=misty_rls_user_id());
ALTER TABLE composio_agent_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE composio_agent_bindings FORCE ROW LEVEL SECURITY;
CREATE POLICY composio_binding_owner ON composio_agent_bindings USING(misty_rls_is_service() OR owner_user_id=misty_rls_user_id()) WITH CHECK(misty_rls_is_service() OR owner_user_id=misty_rls_user_id());
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
 GRANT SELECT,INSERT,UPDATE ON composio_connections,composio_agent_bindings TO misty_app;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
DROP TABLE composio_agent_bindings;
DROP TABLE composio_connections;
