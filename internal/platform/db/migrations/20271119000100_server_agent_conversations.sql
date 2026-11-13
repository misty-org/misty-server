-- +goose Up
-- Spaces and agents. Agent
-- conversations go back on the server: each one belongs to one person in one
-- Space, with its turns, tool output and screenshots. A chat is no longer kept
-- on a device, so kept_on_device_id, the inbox and the legacy copy go.

-- Every conversation belongs to a Space; existing ones join their owner's
-- personal Space. An account with no personal Space to make falls back to any
-- Space it owns, and one that owns none has nowhere to keep them.
UPDATE misty_ask_conversations c SET space_id = misty_ensure_personal_space(c.user_id) WHERE c.space_id IS NULL;
UPDATE misty_ask_conversations c SET space_id = (
    SELECT s.id FROM spaces s WHERE s.owner_user_id = c.user_id
    ORDER BY s.is_default DESC, s.created_at LIMIT 1)
WHERE c.space_id IS NULL;
DELETE FROM misty_ask_conversations WHERE space_id IS NULL;

ALTER TABLE misty_ask_conversations DROP CONSTRAINT misty_ask_conversations_content_space_fkey;
ALTER TABLE misty_ask_conversations ALTER COLUMN space_id SET NOT NULL,
    ADD CONSTRAINT misty_ask_conversations_space_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE CASCADE;
CREATE INDEX misty_ask_conversations_owner_space ON misty_ask_conversations(user_id, space_id, updated_at DESC) WHERE deleted_at IS NULL;

-- +goose StatementBegin
-- A conversation written without a Space lands in its owner's personal Space,
-- and can only live in a Space its owner belongs to.
CREATE FUNCTION misty_conversation_space_guard() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
BEGIN
  IF NEW.space_id IS NULL THEN
    NEW.space_id := misty_ensure_personal_space(NEW.user_id);
  END IF;
  IF TG_OP = 'UPDATE' AND NEW.space_id IS NOT DISTINCT FROM OLD.space_id THEN
    RETURN NEW;
  END IF;
  IF NOT EXISTS(SELECT 1 FROM space_members WHERE space_id = NEW.space_id AND user_id = NEW.user_id) THEN
    RAISE EXCEPTION 'a conversation can only live in a Space its owner belongs to' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER misty_ask_conversations_space BEFORE INSERT OR UPDATE OF space_id ON misty_ask_conversations
    FOR EACH ROW EXECUTE FUNCTION misty_conversation_space_guard();

DROP INDEX IF EXISTS misty_ask_conversations_inbox;
ALTER TABLE misty_ask_conversations DROP COLUMN kept_on_device_id, DROP COLUMN legacy;
-- A chat uploaded from a computer's old local store keeps its own timestamps;
-- retention counts from the upload, so it isn't deleted the moment it arrives.
ALTER TABLE misty_ask_conversations ADD COLUMN imported_at timestamptz;

-- A run names its Space and the profile whose sign-ins it uses.
UPDATE ai_invocations i SET space_id = c.space_id FROM misty_ask_conversations c
    WHERE c.id = i.conversation_id AND i.space_id IS NULL;
ALTER TABLE ai_invocations ADD COLUMN profile text NOT NULL DEFAULT 'personal'
    CHECK (profile ~ '^[A-Za-z0-9_-]{1,100}$');

-- A schedule picks its profile when it's made.
ALTER TABLE workflow_schedules ADD COLUMN profile text NOT NULL DEFAULT 'personal'
    CHECK (profile ~ '^[A-Za-z0-9_-]{1,100}$');

-- Tool output keeps to misty_ask_conversation_events; retention scans it by age.
CREATE INDEX agent_conversation_events_age ON misty_ask_conversation_events(user_id, created_at);

-- Screenshots a tool returned sit beside attachments, in the same object store,
-- but aren't something the person attached.
ALTER TABLE ai_conversation_attachments DROP CONSTRAINT ai_conversation_attachments_scope_check,
    ADD CONSTRAINT ai_conversation_attachments_scope_check CHECK (scope = ANY (ARRAY['conversation'::text, 'visual_query'::text, 'tool_output'::text]));
ALTER TABLE ai_conversation_attachments DROP CONSTRAINT ai_conversation_attachments_check,
    ADD CONSTRAINT ai_conversation_attachments_check CHECK (
        (scope = ANY (ARRAY['conversation'::text, 'tool_output'::text]) AND conversation_id IS NOT NULL)
        OR (scope = 'visual_query'::text AND conversation_id IS NULL AND expires_at IS NOT NULL));

-- A lesson's progress and a course's plan, which lived beside their chats on the device.
CREATE TABLE agent_lesson_documents (
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('lesson', 'course')),
    id text NOT NULL CHECK (char_length(id) BETWEEN 1 AND 256),
    document jsonb NOT NULL CHECK (jsonb_typeof(document) = 'object'),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_user_id, kind, id)
);
ALTER TABLE agent_lesson_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_lesson_documents FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_lesson_documents_owner ON agent_lesson_documents
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR owner_user_id = misty_rls_user_id());
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON agent_lesson_documents TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- A real DM kind. Direct conversations once meant a person and an agent; those
-- are gone (20271028000000). A DM is seen only by the people in it.
ALTER TABLE space_conversations DROP CONSTRAINT space_conversations_kind_check,
    ADD CONSTRAINT space_conversations_kind_check CHECK (kind = ANY (ARRAY['standard'::text, 'direct'::text, 'dm'::text]));
ALTER TABLE space_conversations ADD CONSTRAINT space_conversations_dm_private CHECK (kind <> 'dm' OR NOT visible_to_space);

-- Every private conversation people had becomes a DM, so nothing written in
-- private becomes public. New group chats are public to the Space.
UPDATE space_conversations SET kind = 'dm' WHERE kind = 'standard' AND NOT visible_to_space;

-- +goose StatementBegin
-- Someone who joins a Space joins its group chats.
CREATE FUNCTION misty_space_member_joins_group_chats() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
BEGIN
  INSERT INTO space_conversation_members(conversation_id, user_id, actor_kind)
    SELECT c.id, NEW.user_id, 'person' FROM space_conversations c
    WHERE c.space_id = NEW.space_id AND c.visible_to_space
    ON CONFLICT DO NOTHING;
  RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER space_members_join_group_chats AFTER INSERT ON space_members
    FOR EACH ROW EXECUTE FUNCTION misty_space_member_joins_group_chats();

-- +goose Down
DROP TRIGGER IF EXISTS space_members_join_group_chats ON space_members;
DROP FUNCTION IF EXISTS misty_space_member_joins_group_chats();
UPDATE space_conversations SET kind = 'standard' WHERE kind = 'dm';
ALTER TABLE space_conversations DROP CONSTRAINT space_conversations_dm_private;
ALTER TABLE space_conversations DROP CONSTRAINT space_conversations_kind_check,
    ADD CONSTRAINT space_conversations_kind_check CHECK (kind = ANY (ARRAY['standard'::text, 'direct'::text]));
DROP TABLE IF EXISTS agent_lesson_documents;
DELETE FROM ai_conversation_attachments WHERE scope = 'tool_output';
ALTER TABLE ai_conversation_attachments DROP CONSTRAINT ai_conversation_attachments_check,
    ADD CONSTRAINT ai_conversation_attachments_check CHECK (
        (scope = 'conversation'::text AND conversation_id IS NOT NULL)
        OR (scope = 'visual_query'::text AND conversation_id IS NULL AND expires_at IS NOT NULL));
ALTER TABLE ai_conversation_attachments DROP CONSTRAINT ai_conversation_attachments_scope_check,
    ADD CONSTRAINT ai_conversation_attachments_scope_check CHECK (scope = ANY (ARRAY['conversation'::text, 'visual_query'::text]));
DROP INDEX IF EXISTS agent_conversation_events_age;
ALTER TABLE workflow_schedules DROP COLUMN profile;
ALTER TABLE ai_invocations DROP COLUMN profile;
ALTER TABLE misty_ask_conversations DROP COLUMN imported_at;
ALTER TABLE misty_ask_conversations ADD COLUMN kept_on_device_id text;
ALTER TABLE misty_ask_conversations ADD COLUMN legacy boolean NOT NULL DEFAULT false;
CREATE INDEX misty_ask_conversations_inbox ON misty_ask_conversations(user_id)
    WHERE kept_on_device_id IS NULL AND NOT legacy AND deleted_at IS NULL;
DROP TRIGGER IF EXISTS misty_ask_conversations_space ON misty_ask_conversations;
DROP FUNCTION IF EXISTS misty_conversation_space_guard();
DROP INDEX IF EXISTS misty_ask_conversations_owner_space;
ALTER TABLE misty_ask_conversations DROP CONSTRAINT misty_ask_conversations_space_fkey;
ALTER TABLE misty_ask_conversations ALTER COLUMN space_id DROP NOT NULL,
    ADD CONSTRAINT misty_ask_conversations_content_space_fkey FOREIGN KEY (space_id) REFERENCES spaces(id) ON DELETE SET NULL;
