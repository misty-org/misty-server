-- +goose Up
-- Folders a user makes to group one agent's conversations. Each folder belongs to
-- a single agent; a conversation sits in at most one folder of its own agent, and
-- unfiled conversations stay in the agent's Recents.
CREATE TABLE agent_conversation_folders (
    id text PRIMARY KEY CHECK (id ~ '^folder_[0-9a-f-]{36}$'),
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    agent_id text NOT NULL REFERENCES misty_ask_identities(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 80),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agent_conversation_folders_agent
    ON agent_conversation_folders(owner_user_id, agent_id, created_at);

ALTER TABLE agent_conversation_folders ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_conversation_folders FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_conversation_folders_owner ON agent_conversation_folders
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());

-- Deleting a folder returns its conversations to Recents.
ALTER TABLE misty_ask_conversations
    ADD COLUMN folder_id text REFERENCES agent_conversation_folders(id) ON DELETE SET NULL;
CREATE INDEX misty_ask_conversations_folder ON misty_ask_conversations(folder_id)
    WHERE folder_id IS NOT NULL;

-- Every device showing an agent's sidebar follows its folders and filing from
-- one content-free account topic.
CREATE TRIGGER agent_conversation_folder_account_notify
AFTER INSERT OR UPDATE OR DELETE ON agent_conversation_folders
FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('agent-folders', 'owner_user_id');
CREATE TRIGGER misty_conversation_folder_account_notify
AFTER UPDATE OF folder_id ON misty_ask_conversations
FOR EACH ROW WHEN (OLD.folder_id IS DISTINCT FROM NEW.folder_id)
EXECUTE FUNCTION misty_notify_account_change('agent-folders', 'user_id');

-- +goose Down
DROP TRIGGER IF EXISTS misty_conversation_folder_account_notify ON misty_ask_conversations;
DROP TRIGGER IF EXISTS agent_conversation_folder_account_notify ON agent_conversation_folders;
DROP INDEX IF EXISTS misty_ask_conversations_folder;
ALTER TABLE misty_ask_conversations DROP COLUMN IF EXISTS folder_id;
DROP TABLE IF EXISTS agent_conversation_folders;
