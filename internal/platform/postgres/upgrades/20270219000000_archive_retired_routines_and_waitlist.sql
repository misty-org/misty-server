-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout='5s';
SELECT set_config('app.rls_mode','service',true);
CREATE SCHEMA IF NOT EXISTS misty_archive;
REVOKE ALL ON SCHEMA misty_archive FROM PUBLIC;
-- The old data remains intact, including keys, constraints and timestamps.
-- Runtime roles receive no usage privilege on this operator-only archive.
DO $$ DECLARE relation text; BEGIN
 FOREACH relation IN ARRAY ARRAY['misty_routines','misty_routine_versions','misty_routine_runs','misty_routine_agent_steps','misty_routine_agent_calls','misty_routine_waits','waitlist_signups'] LOOP
  IF to_regclass('public.'||relation) IS NOT NULL THEN
   EXECUTE format('ALTER TABLE public.%I SET SCHEMA misty_archive',relation);
  END IF;
 END LOOP;
END $$;
-- +goose StatementEnd
-- +goose Down
-- Restoring the old code is an explicit operator recovery using the archive.
-- Rollback must not silently re-enable user routines or public waitlist writes.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore retired products only through an explicit recovery migration'; END $$;
-- +goose StatementEnd
