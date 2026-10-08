-- +goose Up
-- Agents on devices (docs/design/agents-on-devices/BRIEF.md): while an agent
-- works on one of the person's computers, that computer stays awake. On by
-- default; the display may still turn off.
ALTER TABLE ai_user_settings ADD COLUMN IF NOT EXISTS keep_awake_for_agents boolean NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE ai_user_settings DROP COLUMN IF EXISTS keep_awake_for_agents;
