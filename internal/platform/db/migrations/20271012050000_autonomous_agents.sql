-- +goose Up
-- Agents act without stopping by default. "Ask before acting for you" turns
-- off for every account; paying gets its own switch, which still asks unless
-- the person turns it off.
ALTER TABLE ai_user_settings ALTER COLUMN app_actions_ask SET DEFAULT false;
UPDATE ai_user_settings SET app_actions_ask = false, updated_at = NOW() WHERE app_actions_ask;
ALTER TABLE ai_user_settings ADD COLUMN payments_ask boolean NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE ai_user_settings DROP COLUMN payments_ask;
ALTER TABLE ai_user_settings ALTER COLUMN app_actions_ask SET DEFAULT true;
