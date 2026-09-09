-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout='5s';
CREATE TABLE misty_routine_runs (
 user_id TEXT NOT NULL,
 routine_id UUID NOT NULL,
 version INTEGER NOT NULL,
 request_id UUID NOT NULL,
 invocation_id TEXT NOT NULL UNIQUE REFERENCES ai_invocations(id) ON DELETE CASCADE,
 execution JSONB NOT NULL CHECK(jsonb_typeof(execution)='object'),
 cancel_requested_at TIMESTAMPTZ,
 outcome TEXT NOT NULL DEFAULT '' CHECK(outcome IN ('','completed','partial','failed','uncertain','cancelled')),
 report JSONB,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(user_id,request_id),
 FOREIGN KEY(user_id,routine_id,version) REFERENCES misty_routine_versions(user_id,routine_id,version) ON DELETE CASCADE
);
CREATE INDEX misty_routine_run_history ON misty_routine_runs(user_id,routine_id,created_at DESC);
ALTER TABLE misty_routine_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE misty_routine_runs FORCE ROW LEVEL SECURITY;
CREATE POLICY owner_policy ON misty_routine_runs FOR ALL
 USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
 WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());
DO $$ BEGIN IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
 GRANT SELECT,INSERT,UPDATE,DELETE ON misty_routine_runs TO misty_app;
END IF;END $$;
CREATE FUNCTION misty_routine_execution_immutable() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.execution IS DISTINCT FROM NEW.execution OR OLD.user_id<>NEW.user_id OR OLD.routine_id<>NEW.routine_id OR OLD.version<>NEW.version OR OLD.request_id<>NEW.request_id OR OLD.invocation_id<>NEW.invocation_id THEN
  RAISE EXCEPTION 'routine execution admission is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER misty_routine_execution_immutable BEFORE UPDATE ON misty_routine_runs
 FOR EACH ROW EXECUTE FUNCTION misty_routine_execution_immutable();
-- +goose StatementEnd
-- +goose Down
-- Keep pinned executions and their reconciliation history during rollback.
SELECT 1;
