-- +goose Up
-- Conversation names are generated after the conversation starts, and a rename
-- on one device should show on the others. One content-free account topic
-- tells every sidebar to reload.
CREATE TRIGGER misty_conversation_title_account_notify
AFTER UPDATE OF title ON misty_ask_conversations
FOR EACH ROW WHEN (OLD.title IS DISTINCT FROM NEW.title)
EXECUTE FUNCTION misty_notify_account_change('conversation-titles', 'user_id');

-- +goose Down
DROP TRIGGER IF EXISTS misty_conversation_title_account_notify ON misty_ask_conversations;
