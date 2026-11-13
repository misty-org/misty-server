-- +goose Up
-- Personal methods are account-owned. Existing Space workflow graphs retain their contract.
CREATE TABLE agent_methods (
 id text PRIMARY KEY, user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 agent_id text NOT NULL, kind text NOT NULL CHECK(kind IN ('workflow','template','skill')),
 enabled boolean NOT NULL DEFAULT true, current_version integer NOT NULL DEFAULT 1,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(id,user_id)
);
CREATE TABLE agent_method_versions (
 id text PRIMARY KEY, method_id text NOT NULL, user_id text NOT NULL, version integer NOT NULL CHECK(version>0),
 definition jsonb NOT NULL CHECK(jsonb_typeof(definition)='object'),
 source_invocation_id text REFERENCES ai_invocations(id) ON DELETE SET NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(method_id,user_id) REFERENCES agent_methods(id,user_id) ON DELETE CASCADE,
 UNIQUE(method_id,version), UNIQUE(id,user_id)
);
CREATE INDEX agent_methods_owner ON agent_methods(user_id,agent_id,updated_at DESC);
ALTER TABLE agent_methods ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_methods FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_methods_owner ON agent_methods USING(misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());
ALTER TABLE agent_method_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_method_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_method_versions_owner ON agent_method_versions USING(misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());
ALTER TABLE scheduled_tasks ADD COLUMN method_version_id text, ADD COLUMN method_inputs jsonb NOT NULL DEFAULT '{}'::jsonb, ADD CONSTRAINT scheduled_method_owner FOREIGN KEY(method_version_id,user_id) REFERENCES agent_method_versions(id,user_id);
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
  GRANT SELECT,INSERT,UPDATE ON agent_methods TO misty_app;
  GRANT SELECT,INSERT ON agent_method_versions TO misty_app;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
ALTER TABLE scheduled_tasks DROP COLUMN method_version_id, DROP COLUMN method_inputs;
DROP TABLE agent_method_versions;
DROP TABLE agent_methods;
