-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

ALTER TABLE personal_agents
    ADD COLUMN system_managed BOOLEAN NOT NULL DEFAULT FALSE;

CREATE UNIQUE INDEX personal_agents_one_managed_misty_per_owner_idx
    ON personal_agents(owner_user_id)
    WHERE system_managed AND deleted_at IS NULL;

CREATE UNIQUE INDEX space_runs_ai_idempotency_key_idx
    ON space_runs(owner_user_id, (input->>'ai_idempotency_key'))
    WHERE trigger_kind='direct_instruction'
      AND COALESCE(input->>'ai_idempotency_key','') <> '';

UPDATE ai_user_settings SET active_companion_agent_id=NULL WHERE active_companion_agent_id IS NOT NULL;
UPDATE ai_surface_preferences SET pinned_agent_id=NULL WHERE pinned_agent_id IS NOT NULL;
-- +goose StatementEnd

-- A managed Misty identity is created lazily after membership and billing
-- checks, so this migration does not enqueue work or choose a model for users.
-- Existing custom Agents remain as read-only migration history.

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS personal_agents_one_managed_misty_per_owner_idx;
DROP INDEX IF EXISTS space_runs_ai_idempotency_key_idx;
ALTER TABLE personal_agents DROP COLUMN IF EXISTS system_managed;
-- +goose StatementEnd
