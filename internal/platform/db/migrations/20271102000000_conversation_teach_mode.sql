-- +goose Up
-- Teach mode: a conversation can be a lesson at the cursor. Its turns run the
-- tutor's fast lane instead of the agent loop.
ALTER TABLE ai_conversation_modes DROP CONSTRAINT IF EXISTS ai_conversation_modes_mode_check;
ALTER TABLE ai_conversation_modes ADD CONSTRAINT ai_conversation_modes_mode_check CHECK (mode IN ('act', 'plan', 'teach'));

-- +goose Down
UPDATE ai_conversation_modes SET mode = 'act' WHERE mode = 'teach';
ALTER TABLE ai_conversation_modes DROP CONSTRAINT IF EXISTS ai_conversation_modes_mode_check;
ALTER TABLE ai_conversation_modes ADD CONSTRAINT ai_conversation_modes_mode_check CHECK (mode IN ('act', 'plan'));
