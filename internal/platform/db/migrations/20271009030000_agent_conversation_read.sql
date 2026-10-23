-- +goose Up
-- When the user last saw a conversation. Its sidebar dot is filled while a run has
-- changed since then and hollow once read. Existing conversations start read.
ALTER TABLE misty_ask_conversations ADD COLUMN last_read_at timestamptz;
UPDATE misty_ask_conversations SET last_read_at = updated_at;

-- Reading on one device clears the dot on the others.
CREATE TRIGGER misty_conversation_read_account_notify
AFTER UPDATE OF last_read_at ON misty_ask_conversations
FOR EACH ROW WHEN (OLD.last_read_at IS DISTINCT FROM NEW.last_read_at)
EXECUTE FUNCTION misty_notify_account_change('conversation-reads', 'user_id');

-- +goose Down
DROP TRIGGER IF EXISTS misty_conversation_read_account_notify ON misty_ask_conversations;
ALTER TABLE misty_ask_conversations DROP COLUMN IF EXISTS last_read_at;
