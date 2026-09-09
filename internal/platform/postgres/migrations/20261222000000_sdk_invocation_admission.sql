-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout='5s';
SELECT set_config('app.rls_mode','service',true);
ALTER TABLE user_app_installations ADD COLUMN authority_generation BIGINT NOT NULL DEFAULT 1 CHECK(authority_generation>0);
ALTER TABLE app_runtime_sessions ADD COLUMN authority_generation BIGINT NOT NULL DEFAULT 1 CHECK(authority_generation>0);
CREATE FUNCTION advance_app_authority_generation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.state IS DISTINCT FROM OLD.state OR NEW.granted_scopes IS DISTINCT FROM OLD.granted_scopes OR NEW.installed_version IS DISTINCT FROM OLD.installed_version OR NEW.permission_version IS DISTINCT FROM OLD.permission_version THEN
  NEW.authority_generation := OLD.authority_generation + 1;
 ELSE
  NEW.authority_generation := OLD.authority_generation;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER app_authority_generation BEFORE UPDATE ON user_app_installations FOR EACH ROW EXECUTE FUNCTION advance_app_authority_generation();

-- Request metadata, not another run engine. The existing invocation owns state,
-- runtime identity, dispatch, history and cancellation.
CREATE TABLE sdk_capability_invocations (
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 request_id UUID NOT NULL,
 caller_app_id TEXT NOT NULL DEFAULT '',
 invocation_id TEXT NOT NULL UNIQUE REFERENCES ai_invocations(id) ON DELETE CASCADE,
 effect_id UUID NOT NULL UNIQUE,
 request JSONB NOT NULL CHECK(jsonb_typeof(request)='object'),
 target_id UUID NOT NULL,
 target_revision INTEGER NOT NULL,
 adapter_version TEXT NOT NULL DEFAULT 'sdk-backend-v1',
 outcome_ciphertext BYTEA,
 outcome_status TEXT NOT NULL DEFAULT '' CHECK(outcome_status IN ('','success','failure','approval_required','device_required','user_intervention_required','uncertain')),
 cancel_requested_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(user_id,caller_app_id,request_id),
 FOREIGN KEY(user_id,target_id,target_revision) REFERENCES sdk_target_versions(user_id,id,revision)
);
ALTER TABLE sdk_capability_invocations ENABLE ROW LEVEL SECURITY;
ALTER TABLE sdk_capability_invocations FORCE ROW LEVEL SECURITY;
CREATE POLICY owner_policy ON sdk_capability_invocations FOR ALL USING(misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
  GRANT SELECT,INSERT,UPDATE,DELETE ON sdk_capability_invocations TO misty_app;
 END IF;
END $$;
ALTER TABLE sdk_capability_invocations ADD COLUMN observed_outcome_ciphertext BYTEA;
ALTER TABLE ai_invocations ADD COLUMN approval_wait_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_run_tool_approvals ALTER COLUMN run_id DROP NOT NULL;
ALTER TABLE agent_run_tool_approvals ADD COLUMN invocation_id TEXT REFERENCES ai_invocations(id) ON DELETE CASCADE;
ALTER TABLE agent_run_tool_approvals ADD CONSTRAINT approval_run_identity CHECK((run_id IS NOT NULL)::int+(invocation_id IS NOT NULL)::int=1);
CREATE UNIQUE INDEX sdk_tool_approval_effect_idx ON agent_run_tool_approvals(invocation_id,tool_call_id) WHERE invocation_id IS NOT NULL;
-- +goose StatementEnd
-- +goose Down
SELECT 1;
