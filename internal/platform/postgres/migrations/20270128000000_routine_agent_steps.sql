-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout='5s';
ALTER TABLE misty_routine_runs ADD CONSTRAINT misty_routine_run_owner UNIQUE(user_id,invocation_id);
CREATE TABLE misty_routine_agent_steps (
 user_id TEXT NOT NULL,
 invocation_id TEXT NOT NULL,
 step_id TEXT NOT NULL,
 call_namespace UUID NOT NULL UNIQUE,
 model_id TEXT NOT NULL,
 max_turns INTEGER NOT NULL CHECK(max_turns BETWEEN 1 AND 20),
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','running','completed','failed','partial','uncertain')),
 output_ciphertext BYTEA,
 completion_fingerprint TEXT,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(user_id,invocation_id,step_id),
 FOREIGN KEY(user_id,invocation_id) REFERENCES misty_routine_runs(user_id,invocation_id) ON DELETE CASCADE
);
-- This is a command-scope record, not a second effect journal. Effects and their
-- encrypted receipts remain in agent_toolbox_action_journal.
CREATE TABLE misty_routine_agent_calls (
 user_id TEXT NOT NULL,
 invocation_id TEXT NOT NULL,
 step_id TEXT NOT NULL,
 call_id TEXT NOT NULL,
 tool_name TEXT NOT NULL,
 arguments_fingerprint TEXT NOT NULL,
 effect_id UUID NOT NULL UNIQUE,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(user_id,invocation_id,call_id),
 FOREIGN KEY(user_id,invocation_id,step_id) REFERENCES misty_routine_agent_steps(user_id,invocation_id,step_id) ON DELETE CASCADE
);
ALTER TABLE agent_model_turn_claims ADD COLUMN routine_usage JSONB;
CREATE FUNCTION misty_routine_agent_pin_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.user_id,NEW.invocation_id,NEW.step_id,NEW.call_namespace,NEW.model_id,NEW.max_turns)
  IS DISTINCT FROM (OLD.user_id,OLD.invocation_id,OLD.step_id,OLD.call_namespace,OLD.model_id,OLD.max_turns) THEN
  RAISE EXCEPTION 'routine agent admission is immutable';
 END IF;
 IF OLD.state NOT IN ('pending','running') AND NEW IS DISTINCT FROM OLD THEN
  RAISE EXCEPTION 'routine agent checkpoint is immutable';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER misty_routine_agent_pin_immutable BEFORE UPDATE ON misty_routine_agent_steps
 FOR EACH ROW EXECUTE FUNCTION misty_routine_agent_pin_immutable();
DO $$ DECLARE name TEXT; BEGIN
 FOREACH name IN ARRAY ARRAY['misty_routine_agent_steps','misty_routine_agent_calls'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',name);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',name);
  EXECUTE format('CREATE POLICY owner_policy ON %I FOR ALL USING(misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id())',name);
  IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
   EXECUTE format('GRANT SELECT,INSERT,UPDATE,DELETE ON %I TO misty_app',name);
  END IF;
 END LOOP;
END $$;
-- +goose StatementEnd
-- +goose Down
-- Preserve pinned checkpoints and scope records during rollback.
SELECT 1;
