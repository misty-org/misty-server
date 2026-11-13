-- +goose Up
-- Preserve the assigned agent even when a task's conversation expires.
ALTER TABLE scheduled_tasks ADD COLUMN agent_id text REFERENCES misty_ask_identities(id) ON DELETE CASCADE;
UPDATE scheduled_tasks t SET agent_id=c.agent_id FROM misty_ask_conversations c
WHERE c.id=t.conversation_id AND c.user_id=t.user_id;

-- +goose Down
ALTER TABLE scheduled_tasks DROP COLUMN agent_id;
