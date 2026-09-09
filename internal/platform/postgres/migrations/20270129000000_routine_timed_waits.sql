-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout='5s';
ALTER TABLE ai_invocations DROP CONSTRAINT ai_invocations_state_check;
ALTER TABLE ai_invocations ADD CONSTRAINT ai_invocations_state_check CHECK(state IN (
 'queued','running','awaiting_approval','awaiting_device','awaiting_intervention','awaiting_timer','completed','failed','canceled'
));
CREATE TABLE misty_routine_waits (
 user_id TEXT NOT NULL,
 invocation_id TEXT NOT NULL,
 step_id TEXT NOT NULL,
 wait_id UUID NOT NULL UNIQUE,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','waiting','completed','expired','cancelled')),
 until_at TIMESTAMPTZ,
 expires_at TIMESTAMPTZ,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(user_id,invocation_id,step_id),
 FOREIGN KEY(user_id,invocation_id) REFERENCES misty_routine_runs(user_id,invocation_id) ON DELETE CASCADE,
 CHECK(state='pending' OR (until_at IS NOT NULL AND expires_at IS NOT NULL))
);
CREATE UNIQUE INDEX misty_routine_one_timer ON misty_routine_waits(invocation_id) WHERE state='waiting';
CREATE INDEX misty_routine_wait_expiry ON misty_routine_waits(expires_at) WHERE state='waiting';
ALTER TABLE misty_routine_waits ENABLE ROW LEVEL SECURITY;
ALTER TABLE misty_routine_waits FORCE ROW LEVEL SECURITY;
CREATE POLICY owner_policy ON misty_routine_waits FOR ALL
 USING(misty_rls_is_service() OR user_id=misty_rls_user_id())
 WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());
DO $$ BEGIN IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
 GRANT SELECT,INSERT,UPDATE,DELETE ON misty_routine_waits TO misty_app;
END IF; END $$;
CREATE FUNCTION misty_routine_wait_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.user_id,NEW.invocation_id,NEW.step_id,NEW.wait_id) IS DISTINCT FROM
    (OLD.user_id,OLD.invocation_id,OLD.step_id,OLD.wait_id) THEN
  RAISE EXCEPTION 'routine wait identity is immutable';
 END IF;
 IF OLD.state<>'pending' AND (NEW.until_at,NEW.expires_at) IS DISTINCT FROM (OLD.until_at,OLD.expires_at) THEN
  RAISE EXCEPTION 'routine wait deadline is immutable';
 END IF;
 IF OLD.state IN ('completed','expired','cancelled') AND NEW IS DISTINCT FROM OLD THEN
  RAISE EXCEPTION 'routine wait is terminal';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER misty_routine_wait_immutable BEFORE UPDATE ON misty_routine_waits
 FOR EACH ROW EXECUTE FUNCTION misty_routine_wait_immutable();
-- +goose StatementEnd
-- +goose Down
-- Preserve sleeping runs and identities for pinned workers during rollback.
SELECT 1;
