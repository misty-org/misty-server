-- +goose Up
-- Chat is people only; agents are reached by @mention. Agent chats go back to
-- being the agent runtime's own conversations in misty_ask_conversations, and
-- stop being mirrored as direct conversations in the owner's personal Space.
-- The triggers go first: deleting a Space conversation used to delete the
-- agent chat behind it.
DROP TRIGGER IF EXISTS misty_space_dm_agent_chat ON space_conversations;
DROP TRIGGER IF EXISTS misty_agent_chat_space_dm ON misty_ask_conversations;
DROP FUNCTION IF EXISTS misty_space_dm_agent_chat();
DROP FUNCTION IF EXISTS misty_agent_chat_space_dm();
DROP FUNCTION IF EXISTS misty_chat_title(text);
DROP FUNCTION IF EXISTS misty_chat_agent(text, text);

-- Direct conversations only ever held an agent chat (the check constraint
-- requires an agent), so every one is a mirror. The agent chats themselves stay.
DELETE FROM space_conversations WHERE kind = 'direct';

-- +goose Down
SELECT 1;
