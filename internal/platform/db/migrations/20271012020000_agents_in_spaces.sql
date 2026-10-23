-- +goose Up
-- Agents live in Spaces. Every account has a personal Space, every agent
-- belongs to exactly one Space, and the agent's listing in that Space is its
-- membership there: it says whose agent it is and how it takes requests.

-- +goose StatementBegin
-- The personal Space is the account's protected default Space. Accounts that
-- already have one keep it under its current name.
CREATE FUNCTION misty_ensure_personal_space(target_user_id text) RETURNS text
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
DECLARE
  existing text;
  first_name text;
  new_space_id text := 'space_' || gen_random_uuid()::text;
  new_domain_id text := 'sd_' || gen_random_uuid()::text;
BEGIN
  -- Same lock the app takes when it creates a Space for this owner.
  PERFORM pg_advisory_xact_lock(hashtext('spaces:owner:' || target_user_id));
  SELECT id INTO existing FROM spaces
    WHERE owner_user_id=target_user_id AND is_default AND lifecycle_state='active';
  IF existing IS NOT NULL THEN
    RETURN existing;
  END IF;
  SELECT left(NULLIF(split_part(btrim(name), ' ', 1), ''), 64) INTO first_name
    FROM users WHERE id=target_user_id AND lifecycle_state='active';
  IF NOT FOUND THEN
    RETURN NULL;
  END IF;
  INSERT INTO security_domains(id,kind,owner_user_id,space_id) VALUES(new_domain_id,'space',target_user_id,new_space_id);
  INSERT INTO spaces(id,owner_user_id,name,security_domain_id,is_default)
    VALUES(new_space_id,target_user_id,COALESCE(first_name || '''s Space','Personal Space'),new_domain_id,TRUE);
  INSERT INTO space_storage_usage(space_id) VALUES(new_space_id);
  INSERT INTO space_members(space_id,user_id,role) VALUES(new_space_id,target_user_id,'owner');
  INSERT INTO space_roles(id,space_id,name,is_everyone,permissions) VALUES('role_' || gen_random_uuid()::text,new_space_id,'@everyone',TRUE,
    '["space.view","messages.read","messages.write","attachments.upload","library.view","library.upload","library.add","library.edit","library.download","library.import","storage.view_own_usage","tasks.view","tasks.manage"]'::jsonb);
  RETURN new_space_id;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
-- New accounts get their personal Space at sign-up.
CREATE FUNCTION misty_user_personal_space() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
BEGIN
  PERFORM misty_ensure_personal_space(NEW.id);
  RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER users_personal_space AFTER INSERT ON users
    FOR EACH ROW EXECUTE FUNCTION misty_user_personal_space();

SELECT misty_ensure_personal_space(id) FROM users WHERE lifecycle_state='active';

-- Every live agent names its Space. A deleted agent may keep the Space it
-- last lived in, or none once that Space is gone.
ALTER TABLE misty_ask_identities ADD COLUMN space_id text REFERENCES spaces(id) ON DELETE SET NULL;
UPDATE misty_ask_identities a SET space_id=s.id FROM spaces s
    WHERE s.owner_user_id=a.owner_user_id AND s.is_default AND s.lifecycle_state='active';
-- Accounts on their way out have no active personal Space; their agents go
-- with them.
UPDATE misty_ask_identities SET deleted_at=COALESCE(deleted_at,NOW()),enabled=FALSE,updated_at=NOW() WHERE space_id IS NULL;
ALTER TABLE misty_ask_identities ADD CONSTRAINT misty_ask_identities_space_required CHECK (space_id IS NOT NULL OR deleted_at IS NOT NULL);
CREATE INDEX misty_ask_identities_space ON misty_ask_identities(space_id) WHERE deleted_at IS NULL;

-- +goose StatementBegin
-- New agents default to the owner's personal Space, and an agent can only
-- live in a Space its owner belongs to.
CREATE FUNCTION misty_agent_space_guard() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
BEGIN
  IF NEW.space_id IS NULL AND NEW.deleted_at IS NULL THEN
    NEW.space_id := misty_ensure_personal_space(NEW.owner_user_id);
  END IF;
  IF NEW.space_id IS NOT NULL AND NEW.deleted_at IS NULL
     AND (TG_OP='INSERT' OR NEW.space_id IS DISTINCT FROM OLD.space_id)
     AND NOT EXISTS(SELECT 1 FROM space_members m JOIN spaces s ON s.id=m.space_id AND s.lifecycle_state='active'
       WHERE m.space_id=NEW.space_id AND m.user_id=NEW.owner_user_id) THEN
    RAISE EXCEPTION 'an agent can only live in a Space its owner belongs to' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER misty_agent_space_guard BEFORE INSERT OR UPDATE OF space_id ON misty_ask_identities
    FOR EACH ROW EXECUTE FUNCTION misty_agent_space_guard();

-- +goose StatementBegin
-- An agent's listing follows it. A move starts over at "ask" because the new
-- Space has different people in it.
CREATE FUNCTION misty_agent_space_listing() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
BEGIN
  DELETE FROM space_agent_listings WHERE agent_id=NEW.id AND space_id IS DISTINCT FROM NEW.space_id;
  IF NEW.space_id IS NOT NULL AND NEW.deleted_at IS NULL THEN
    INSERT INTO space_agent_listings(space_id,agent_id,owner_user_id) VALUES(NEW.space_id,NEW.id,NEW.owner_user_id)
      ON CONFLICT (space_id,agent_id) DO NOTHING;
  END IF;
  RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER misty_agent_space_listing AFTER INSERT OR UPDATE OF space_id, deleted_at ON misty_ask_identities
    FOR EACH ROW EXECUTE FUNCTION misty_agent_space_listing();

-- Listings were per Space an owner chose to publish to. Now there is exactly
-- one, in the agent's own Space; an existing policy there is kept.
DELETE FROM space_agent_listings l USING misty_ask_identities a
    WHERE a.id=l.agent_id AND (a.deleted_at IS NOT NULL OR l.space_id IS DISTINCT FROM a.space_id);
INSERT INTO space_agent_listings(space_id,agent_id,owner_user_id)
    SELECT space_id,id,owner_user_id FROM misty_ask_identities WHERE space_id IS NOT NULL AND deleted_at IS NULL
    ON CONFLICT (space_id,agent_id) DO NOTHING;

-- +goose StatementBegin
-- Agents go back to their owner's personal Space when the owner leaves a
-- Space or the Space is deleted. An agent with no personal Space to go to
-- belongs to an account being deleted and is retired with it.
CREATE FUNCTION misty_rehome_space_agents(from_space text, only_owner text, retire_homeless boolean) RETURNS void
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
BEGIN
  UPDATE misty_ask_identities a SET space_id=home.id,updated_at=NOW()
    FROM spaces home
    WHERE a.space_id=from_space AND a.deleted_at IS NULL AND (only_owner IS NULL OR a.owner_user_id=only_owner)
      AND home.owner_user_id=a.owner_user_id AND home.is_default AND home.lifecycle_state='active' AND home.id<>from_space;
  IF retire_homeless THEN
    UPDATE misty_ask_identities SET deleted_at=NOW(),enabled=FALSE,updated_at=NOW()
      WHERE space_id=from_space AND deleted_at IS NULL AND (only_owner IS NULL OR owner_user_id=only_owner);
  END IF;
END $$;

CREATE FUNCTION misty_space_agents_leave() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
BEGIN
  IF TG_TABLE_NAME='space_members' THEN
    PERFORM misty_rehome_space_agents(OLD.space_id, OLD.user_id, FALSE);
    RETURN NULL;
  END IF;
  IF TG_OP='DELETE' THEN
    PERFORM misty_rehome_space_agents(OLD.id, NULL, TRUE);
    RETURN OLD;
  END IF;
  IF OLD.lifecycle_state='active' AND NEW.lifecycle_state<>'active' THEN
    PERFORM misty_rehome_space_agents(NEW.id, NULL, TRUE);
  END IF;
  RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER space_members_rehome_agents AFTER DELETE ON space_members
    FOR EACH ROW EXECUTE FUNCTION misty_space_agents_leave();
CREATE TRIGGER spaces_rehome_agents_delete BEFORE DELETE ON spaces
    FOR EACH ROW EXECUTE FUNCTION misty_space_agents_leave();
CREATE TRIGGER spaces_rehome_agents_update AFTER UPDATE OF lifecycle_state ON spaces
    FOR EACH ROW EXECUTE FUNCTION misty_space_agents_leave();

-- A person can @mention an agent in Space chat. The request remembers the
-- message so the agent's reply lands under it.
ALTER TABLE agent_member_requests
    ADD COLUMN source_message_id text,
    ADD COLUMN source_conversation_id text;

-- Files agents make are saved to the agent's Space as Library items. The
-- upload carries the agent's provenance until the item exists.
ALTER TABLE space_library_uploads
    ADD COLUMN agent_id text REFERENCES misty_ask_identities(id) ON DELETE SET NULL,
    ADD COLUMN agent_conversation_id text CHECK (char_length(agent_conversation_id) <= 200),
    ADD COLUMN agent_run_id text CHECK (char_length(agent_run_id) <= 200),
    ADD COLUMN agent_artifact_kind text CHECK (agent_artifact_kind IN ('download','file','image','code','document'));

CREATE TABLE space_agent_artifacts (
    item_id text PRIMARY KEY REFERENCES space_library_items(id) ON DELETE CASCADE,
    space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    agent_id text REFERENCES misty_ask_identities(id) ON DELETE SET NULL,
    -- Kept so the item still says which agent made it after the agent is gone.
    agent_name text NOT NULL CHECK (char_length(agent_name) BETWEEN 1 AND 80),
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- The owner's private conversation and run. Only the owner sees these.
    conversation_id text NOT NULL DEFAULT '',
    run_id text NOT NULL DEFAULT '',
    kind text NOT NULL CHECK (kind IN ('download','file','image','code','document')),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX space_agent_artifacts_space ON space_agent_artifacts(space_id, created_at DESC);
CREATE INDEX space_agent_artifacts_agent ON space_agent_artifacts(agent_id, created_at DESC);
ALTER TABLE space_agent_artifacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_agent_artifacts FORCE ROW LEVEL SECURITY;
CREATE POLICY space_agent_artifacts_access ON space_agent_artifacts
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service());

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_agent_artifacts TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS space_agent_artifacts;
ALTER TABLE space_library_uploads
    DROP COLUMN IF EXISTS agent_artifact_kind,
    DROP COLUMN IF EXISTS agent_run_id,
    DROP COLUMN IF EXISTS agent_conversation_id,
    DROP COLUMN IF EXISTS agent_id;
ALTER TABLE agent_member_requests
    DROP COLUMN IF EXISTS source_conversation_id,
    DROP COLUMN IF EXISTS source_message_id;
DROP TRIGGER IF EXISTS spaces_rehome_agents_update ON spaces;
DROP TRIGGER IF EXISTS spaces_rehome_agents_delete ON spaces;
DROP TRIGGER IF EXISTS space_members_rehome_agents ON space_members;
DROP FUNCTION IF EXISTS misty_space_agents_leave();
DROP FUNCTION IF EXISTS misty_rehome_space_agents(text, text, boolean);
DROP TRIGGER IF EXISTS misty_agent_space_listing ON misty_ask_identities;
DROP FUNCTION IF EXISTS misty_agent_space_listing();
DROP TRIGGER IF EXISTS misty_agent_space_guard ON misty_ask_identities;
DROP FUNCTION IF EXISTS misty_agent_space_guard();
ALTER TABLE misty_ask_identities DROP COLUMN IF EXISTS space_id;
DROP TRIGGER IF EXISTS users_personal_space ON users;
DROP FUNCTION IF EXISTS misty_user_personal_space();
DROP FUNCTION IF EXISTS misty_ensure_personal_space(text);
