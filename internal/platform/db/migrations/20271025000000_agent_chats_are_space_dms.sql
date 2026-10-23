-- +goose Up
-- One chat system. Every chat with an agent is a direct conversation in its
-- owner's personal Space, under the same id, so it appears beside the Space's
-- channels and people. The agent runtime keeps its per-chat settings (model,
-- mode, session state) in misty_ask_conversations, now a one-to-one sidecar
-- of the Space conversation, and triggers keep the two in step.

-- A person can have many chats with the same agent.
DROP INDEX IF EXISTS space_conversations_direct_unique;

-- Earlier Space-chat conversations with an agent had no runs behind them;
-- their messages go with them.
DELETE FROM space_conversations c WHERE c.kind='direct'
    AND NOT EXISTS(SELECT 1 FROM misty_ask_conversations m WHERE m.id=c.id);

-- +goose StatementBegin
-- The agent a chat talks to: its own agent, or the owner's built-in Misty.
CREATE FUNCTION misty_chat_agent(chat_agent text, owner text) RETURNS text
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
  SELECT id FROM misty_ask_identities
  WHERE owner_user_id=owner AND deleted_at IS NULL
    AND ((chat_agent IS NOT NULL AND id=chat_agent) OR (chat_agent IS NULL AND system_managed))
  ORDER BY created_at LIMIT 1
$$;

CREATE FUNCTION misty_chat_title(raw text) RETURNS text
    LANGUAGE sql IMMUTABLE
AS $$ SELECT COALESCE(NULLIF(left(btrim(COALESCE(raw,'')),80),''),'New chat') $$;

-- Creates, renames, re-points and removes the Space conversation for an agent chat.
CREATE FUNCTION misty_agent_chat_space_dm() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
DECLARE
  home text;
  agent text;
BEGIN
  IF NEW.deleted_at IS NOT NULL THEN
    DELETE FROM space_conversations WHERE id=NEW.id;
    RETURN NULL;
  END IF;
  agent := misty_chat_agent(NEW.agent_id, NEW.user_id);
  IF agent IS NULL THEN
    RETURN NULL;
  END IF;
  IF EXISTS(SELECT 1 FROM space_conversations WHERE id=NEW.id) THEN
    UPDATE space_conversations SET title=misty_chat_title(NEW.title),direct_agent_id=agent,updated_at=GREATEST(updated_at,NEW.updated_at)
      WHERE id=NEW.id AND (title IS DISTINCT FROM misty_chat_title(NEW.title) OR direct_agent_id IS DISTINCT FROM agent OR updated_at<NEW.updated_at);
    UPDATE space_conversation_members SET agent_id=agent
      WHERE conversation_id=NEW.id AND actor_kind='agent' AND agent_id IS DISTINCT FROM agent;
    RETURN NULL;
  END IF;
  home := misty_ensure_personal_space(NEW.user_id);
  IF home IS NULL THEN
    RETURN NULL;
  END IF;
  INSERT INTO space_conversations(id,space_id,title,created_by_user_id,created_at,updated_at,kind,direct_user_id,direct_agent_id)
    VALUES(NEW.id,home,misty_chat_title(NEW.title),NEW.user_id,NEW.created_at,NEW.updated_at,'direct',NEW.user_id,agent);
  INSERT INTO space_conversation_members(conversation_id,user_id,actor_kind) VALUES(NEW.id,NEW.user_id,'person');
  INSERT INTO space_conversation_members(conversation_id,agent_id,actor_kind) VALUES(NEW.id,agent,'agent');
  RETURN NULL;
END $$;

-- Deleting the Space conversation deletes the agent chat.
CREATE FUNCTION misty_space_dm_agent_chat() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
BEGIN
  UPDATE misty_ask_conversations SET deleted_at=NOW(),updated_at=NOW() WHERE id=OLD.id AND deleted_at IS NULL;
  RETURN OLD;
END $$;
-- +goose StatementEnd

CREATE TRIGGER misty_agent_chat_space_dm AFTER INSERT OR UPDATE OF title, agent_id, deleted_at, updated_at ON misty_ask_conversations
    FOR EACH ROW EXECUTE FUNCTION misty_agent_chat_space_dm();
CREATE TRIGGER misty_space_dm_agent_chat AFTER DELETE ON space_conversations
    FOR EACH ROW WHEN (OLD.kind = 'direct') EXECUTE FUNCTION misty_space_dm_agent_chat();

-- Existing chats become DMs in their owner's personal Space.
SELECT misty_ensure_personal_space(user_id) FROM (SELECT DISTINCT user_id FROM misty_ask_conversations WHERE deleted_at IS NULL) owners;
INSERT INTO space_conversations(id,space_id,title,created_by_user_id,created_at,updated_at,kind,direct_user_id,direct_agent_id)
    SELECT c.id,s.id,misty_chat_title(c.title),c.user_id,c.created_at,c.updated_at,'direct',c.user_id,misty_chat_agent(c.agent_id,c.user_id)
    FROM misty_ask_conversations c
    JOIN spaces s ON s.owner_user_id=c.user_id AND s.is_default AND s.lifecycle_state='active'
    WHERE c.deleted_at IS NULL AND misty_chat_agent(c.agent_id,c.user_id) IS NOT NULL
    ON CONFLICT (id) DO NOTHING;
INSERT INTO space_conversation_members(conversation_id,user_id,actor_kind)
    SELECT c.id,c.direct_user_id,'person' FROM space_conversations c
    JOIN misty_ask_conversations m ON m.id=c.id
    ON CONFLICT DO NOTHING;
INSERT INTO space_conversation_members(conversation_id,agent_id,actor_kind)
    SELECT c.id,c.direct_agent_id,'agent' FROM space_conversations c
    JOIN misty_ask_conversations m ON m.id=c.id
    ON CONFLICT DO NOTHING;

-- +goose Down
DROP TRIGGER IF EXISTS misty_space_dm_agent_chat ON space_conversations;
DROP TRIGGER IF EXISTS misty_agent_chat_space_dm ON misty_ask_conversations;
DROP FUNCTION IF EXISTS misty_space_dm_agent_chat();
DROP FUNCTION IF EXISTS misty_agent_chat_space_dm();
DROP FUNCTION IF EXISTS misty_chat_title(text);
DROP FUNCTION IF EXISTS misty_chat_agent(text, text);
