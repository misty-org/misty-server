-- +goose Up
-- Group DMs and threads.
--
-- A thread reply is a message with thread_root_id set: it lives in the thread's panel, not in the
-- channel's own list. An inline reply (reply_to_message_id) is unchanged and stays in the list.
ALTER TABLE space_messages ADD COLUMN thread_root_id text REFERENCES space_messages(id) ON DELETE CASCADE;
CREATE INDEX space_messages_thread_idx ON space_messages (thread_root_id, seq) WHERE thread_root_id IS NOT NULL;

-- The threads a person follows (started or replied to), and how far they have read in each.
CREATE TABLE space_thread_reads (
  user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  thread_root_id text NOT NULL REFERENCES space_messages(id) ON DELETE CASCADE,
  read_message_seq bigint NOT NULL DEFAULT 0,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, thread_root_id)
);
CREATE INDEX space_thread_reads_root_idx ON space_thread_reads (thread_root_id);

-- The agents a conversation lists, each under the person who owns it. The conversation keeps an id and
-- a name and nothing else; only the owner can ask the agent to act. A private group's roster is its own:
-- adding an agent here never lists it in the Space.
CREATE TABLE space_conversation_agents (
  conversation_id text NOT NULL REFERENCES space_conversations(id) ON DELETE CASCADE,
  agent_id text NOT NULL CHECK (char_length(agent_id) BETWEEN 1 AND 200),
  owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  agent_name text NOT NULL CHECK (char_length(agent_name) BETWEEN 1 AND 320),
  added_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (conversation_id, owner_user_id, agent_id)
);
CREATE INDEX space_conversation_agents_owner_idx ON space_conversation_agents (owner_user_id);

ALTER TABLE space_thread_reads ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_thread_reads FORCE ROW LEVEL SECURITY;
CREATE POLICY space_thread_reads_policy ON space_thread_reads
  USING (public.misty_rls_is_service() OR user_id = public.misty_rls_user_id())
  WITH CHECK (public.misty_rls_is_service() OR user_id = public.misty_rls_user_id());

ALTER TABLE space_conversation_agents ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_conversation_agents FORCE ROW LEVEL SECURITY;
CREATE POLICY space_conversation_agents_read ON space_conversation_agents FOR SELECT
  USING (public.misty_rls_is_service() OR public.misty_is_space_conversation_member(conversation_id));
CREATE POLICY space_conversation_agents_owner_write ON space_conversation_agents
  USING (public.misty_rls_is_service() OR owner_user_id = public.misty_rls_user_id())
  WITH CHECK (public.misty_rls_is_service() OR (
    owner_user_id = public.misty_rls_user_id() AND public.misty_is_space_conversation_member(conversation_id)));

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_thread_reads, space_conversation_agents TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS space_conversation_agents;
DROP TABLE IF EXISTS space_thread_reads;
DROP INDEX IF EXISTS space_messages_thread_idx;
ALTER TABLE space_messages DROP COLUMN IF EXISTS thread_root_id;
