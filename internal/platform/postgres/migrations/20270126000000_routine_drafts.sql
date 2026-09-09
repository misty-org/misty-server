-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode','service',true);
CREATE TABLE misty_routines (
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 id UUID NOT NULL,
 space_id TEXT REFERENCES spaces(id) ON DELETE CASCADE,
 current_version INTEGER NOT NULL CHECK(current_version>0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(user_id,id)
);
CREATE TABLE misty_routine_versions (
 user_id TEXT NOT NULL,
 routine_id UUID NOT NULL,
 version INTEGER NOT NULL CHECK(version>0),
 definition JSONB NOT NULL CHECK(jsonb_typeof(definition)='object' AND octet_length(definition::text)<=2097152),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(user_id,routine_id,version),
 FOREIGN KEY(user_id,routine_id) REFERENCES misty_routines(user_id,id) ON DELETE CASCADE
);
ALTER TABLE misty_routines ADD CONSTRAINT misty_routine_current_version
 FOREIGN KEY(user_id,id,current_version) REFERENCES misty_routine_versions(user_id,routine_id,version) DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX misty_routine_space ON misty_routines(user_id,space_id,id);
-- Draft writes cannot create enabled routines or execution authority. Future
-- enablement stores a separate reviewed version and scoped consent record.
DO $$ DECLARE name TEXT; BEGIN
 FOREACH name IN ARRAY ARRAY['misty_routines','misty_routine_versions'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',name);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',name);
  EXECUTE format('CREATE POLICY owner_policy ON %I FOR ALL USING(misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id())',name);
 END LOOP;
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
  GRANT SELECT,INSERT,UPDATE,DELETE ON misty_routines TO misty_app;
  GRANT SELECT,INSERT,DELETE ON misty_routine_versions TO misty_app;
 END IF;
END $$;
-- Reject accidental version edits even for the service role. Deletion remains
-- available to account/Space teardown through the parent foreign key.
CREATE FUNCTION misty_routine_version_immutable() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'routine versions are immutable' USING ERRCODE='23514'; END;
$$;
CREATE TRIGGER misty_routine_version_immutable BEFORE UPDATE ON misty_routine_versions
 FOR EACH ROW EXECUTE FUNCTION misty_routine_version_immutable();
-- +goose StatementEnd
-- +goose Down
-- Preserve reviewed definitions and history on rollback.
SELECT 1;
