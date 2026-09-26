-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);
-- Keep legacy columns and records for compatibility and provenance. Deleting a
-- content destination must not delete account-owned conversations or work.
DO $$
DECLARE item record; relation text;
BEGIN
  FOREACH relation IN ARRAY ARRAY['misty_agent_execution_leases','misty_ask_conversations','misty_memories','ai_invocations','ai_invocation_contexts','space_runs','agent_run_jobs','agent_run_contexts'] LOOP
    EXECUTE format('ALTER TABLE %I ALTER COLUMN space_id DROP NOT NULL', relation);
    FOR item IN SELECT c.conname FROM pg_constraint c
      WHERE c.conrelid=relation::regclass AND c.contype='f'
        AND c.confrelid='spaces'::regclass LOOP
      EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', relation, item.conname);
    END LOOP;
    EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I FOREIGN KEY(space_id) REFERENCES spaces(id) ON DELETE SET NULL', relation, relation||'_content_space_fkey');
  END LOOP;
END $$;
UPDATE misty_agent_execution_leases SET space_id=NULL;
-- Shared workflow records keep their existing policy; Agent history is private
-- to its account even when the run was originally launched from shared content.
DROP POLICY IF EXISTS space_runs_private_or_shared_policy ON space_runs;
CREATE POLICY space_runs_private_or_shared_policy ON space_runs FOR ALL
  USING(misty_rls_is_service() OR (agent_id IS NOT NULL AND owner_user_id=misty_rls_user_id())
    OR (agent_id IS NULL AND (requesting_member_id=misty_rls_user_id() OR
      misty_is_shared_space_run_visible(space_id,source_type,source_conversation_id))))
  WITH CHECK(misty_rls_is_service() OR (agent_id IS NOT NULL AND owner_user_id=misty_rls_user_id())
    OR (agent_id IS NULL AND (requesting_member_id=misty_rls_user_id() OR
      misty_is_shared_space_run_visible(space_id,source_type,source_conversation_id))));
-- +goose StatementEnd

-- +goose Down
-- Reintroducing required content ownership would orphan personal work.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Account Agents require a forward migration'; END $$;
-- +goose StatementEnd
