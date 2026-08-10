-- +goose Up
-- +goose StatementBegin
-- Remove legacy messaging data before narrowing the durable agent constraints.
-- Job dependencies use cascading foreign keys, so no orphaned run data remains.
DROP TABLE IF EXISTS discord_response_destinations CASCADE;
DROP TABLE IF EXISTS discord_interactions CASCADE;
DROP TABLE IF EXISTS discord_oauth_states CASCADE;
DROP TABLE IF EXISTS discord_installations CASCADE;
DROP TABLE IF EXISTS discord_identities CASCADE;

DELETE FROM agent_jobs WHERE trigger_kind = 'discord';
DELETE FROM agent_triggers WHERE kind = 'discord';

ALTER TABLE agent_triggers
    DROP CONSTRAINT IF EXISTS agent_triggers_kind_check;
ALTER TABLE agent_triggers
    ADD CONSTRAINT agent_triggers_kind_check
    CHECK (kind IN ('manual','schedule','file_created','file_changed','local_webhook'));

ALTER TABLE agent_jobs
    DROP CONSTRAINT IF EXISTS agent_jobs_trigger_kind_check;
ALTER TABLE agent_jobs
    ADD CONSTRAINT agent_jobs_trigger_kind_check
    CHECK (trigger_kind IN ('manual','schedule','file_created','file_changed','local_webhook'));
-- +goose StatementEnd

-- +goose Down
-- Cleanup of removed integration data is intentionally irreversible.
SELECT 1;
