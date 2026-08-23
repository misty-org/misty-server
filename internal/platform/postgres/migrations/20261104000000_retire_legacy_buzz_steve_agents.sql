-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SELECT set_config('app.rls_mode', 'service', true);

-- Buzz and Steve were pre-beta Space-agent personas. They were copied into the
-- personal Agent catalog during the creator-scoped migration and should not be
-- presented as current Misty Agents. Limit this cleanup to migrated legacy
-- records so a new user-created Agent with the same name is left untouched.
UPDATE space_runs
SET state='canceled',
    error_code='legacy_agent_retired',
    error_message='Legacy Agent retired in favor of Misty Agents',
    runtime_phase='canceled',
    canceled_at=NOW(),
    completed_at=NOW(),
    updated_at=NOW()
WHERE agent_id IN (
    SELECT id FROM personal_agents
    WHERE source_space_agent_id IS NOT NULL
      AND lower(trim(name)) IN ('buzz','steve')
)
AND state IN ('queued','running','awaiting_approval','awaiting_device','cooldown','retrying');

UPDATE ai_user_settings
SET active_companion_agent_id=NULL,updated_at=NOW()
WHERE active_companion_agent_id IN (
    SELECT id FROM personal_agents
    WHERE source_space_agent_id IS NOT NULL
      AND lower(trim(name)) IN ('buzz','steve')
);

UPDATE personal_agents
SET enabled=FALSE,deleted_at=NOW(),version=version+1,updated_at=NOW()
WHERE source_space_agent_id IS NOT NULL
  AND lower(trim(name)) IN ('buzz','steve')
  AND deleted_at IS NULL;

UPDATE space_agents
SET enabled=FALSE,status='disabled',schedules_enabled=FALSE,updated_at=NOW()
WHERE lower(trim(name)) IN ('buzz','steve');
-- +goose StatementEnd

-- Retiring ambiguous legacy identities is intentionally one-way. Their run
-- and version history remains available for audit.
-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
