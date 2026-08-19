-- +goose Up
-- +goose StatementBegin
-- Chained voice preferences for creator-owned companion Agents. Conversation
-- messages remain the canonical transcript; raw microphone audio is never
-- persisted by Misty.
ALTER TABLE personal_agents
    ADD COLUMN voice_id TEXT NOT NULL DEFAULT 'alloy';

ALTER TABLE personal_agent_versions
    ADD COLUMN voice_id TEXT NOT NULL DEFAULT 'alloy';

-- A client nonce may replay message creation, but one canonical user turn must
-- still create only one first-attempt Agent run. Retries have trigger_kind
-- 'retry' and intentionally remain separate linked runs.
CREATE UNIQUE INDEX space_runs_agent_source_message_once
    ON space_runs(source_message_id, agent_id)
    WHERE source_message_id IS NOT NULL AND trigger_kind = 'direct_instruction';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS space_runs_agent_source_message_once;
ALTER TABLE personal_agent_versions DROP COLUMN IF EXISTS voice_id;
ALTER TABLE personal_agents DROP COLUMN IF EXISTS voice_id;
-- +goose StatementEnd
