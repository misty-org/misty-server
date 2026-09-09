-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout='5s';
SELECT set_config('app.rls_mode','service',true);

-- Immutable admission-time scope for conversational runs. A later target change,
-- installation or delegation cannot expand a run's available implementations.
CREATE TABLE agent_sdk_capability_bindings (
 run_id TEXT NOT NULL REFERENCES space_runs(id) ON DELETE CASCADE,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 target_id UUID NOT NULL,
 target_revision INTEGER NOT NULL,
 capability TEXT NOT NULL,
 capability_version INTEGER NOT NULL CHECK(capability_version>0),
 provider_id TEXT NOT NULL,
 provider_version INTEGER NOT NULL CHECK(provider_version>0),
 adapter_version TEXT NOT NULL DEFAULT 'sdk-backend-v1',
 PRIMARY KEY(run_id,target_id,capability),
 FOREIGN KEY(user_id,target_id,target_revision) REFERENCES sdk_target_versions(user_id,id,revision),
 FOREIGN KEY(user_id,provider_id,provider_version) REFERENCES sdk_provider_versions(user_id,provider_id,version)
);
ALTER TABLE agent_sdk_capability_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_sdk_capability_bindings FORCE ROW LEVEL SECURITY;
CREATE POLICY owner_policy ON agent_sdk_capability_bindings FOR ALL
 USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
 WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
  GRANT SELECT,INSERT ON agent_sdk_capability_bindings TO misty_app;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
-- Pinned bindings are recovery records; disable new execution through rollout gates.
SELECT 1;
