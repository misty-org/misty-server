-- +goose Up
-- Agents belong to a Space and to the person who made them. space_id is the
-- agent's home Space; space_agents stays for membership in other Spaces.
ALTER TABLE misty_ask_identities ADD COLUMN space_id text REFERENCES spaces(id) ON DELETE CASCADE;

UPDATE misty_ask_identities a SET space_id = misty_ensure_personal_space(a.owner_user_id) WHERE a.space_id IS NULL;
-- A paused or closed account has no personal Space to make; its agents fall
-- back to any Space it still owns.
UPDATE misty_ask_identities a SET space_id = (
    SELECT s.id FROM spaces s WHERE s.owner_user_id = a.owner_user_id
    ORDER BY s.is_default DESC, s.created_at LIMIT 1)
WHERE a.space_id IS NULL;
-- Agents of accounts that own no Space at all can't belong anywhere.
DELETE FROM misty_ask_identities WHERE space_id IS NULL;
ALTER TABLE misty_ask_identities ALTER COLUMN space_id SET NOT NULL;
CREATE INDEX misty_ask_identities_home_space ON misty_ask_identities(space_id, owner_user_id) WHERE deleted_at IS NULL;

-- +goose StatementBegin
-- A new agent without a home starts in its owner's personal Space. A home must
-- be a Space the owner belongs to.
CREATE FUNCTION misty_agent_home_space_guard() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
BEGIN
  IF NEW.space_id IS NULL THEN
    NEW.space_id := misty_ensure_personal_space(NEW.owner_user_id);
  END IF;
  IF (TG_OP = 'INSERT' OR NEW.space_id IS DISTINCT FROM OLD.space_id)
     AND NOT EXISTS (SELECT 1 FROM space_members WHERE space_id = NEW.space_id AND user_id = NEW.owner_user_id) THEN
    RAISE EXCEPTION 'an agent''s home Space must be one its owner belongs to' USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END $$;

-- New agents are members of their home Space; a deleted agent leaves every Space.
CREATE OR REPLACE FUNCTION misty_agent_space_membership() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
BEGIN
  IF NEW.deleted_at IS NOT NULL THEN
    DELETE FROM space_agents WHERE agent_id=NEW.id;
    RETURN NULL;
  END IF;
  IF TG_OP='INSERT' OR NEW.space_id IS DISTINCT FROM OLD.space_id THEN
    INSERT INTO space_agents(space_id,agent_id,owner_user_id) VALUES(NEW.space_id,NEW.id,NEW.owner_user_id)
      ON CONFLICT DO NOTHING;
  END IF;
  RETURN NULL;
END $$;

-- When the owner leaves a Space, or the Space goes, agents homed there move to
-- the owner's personal Space.
CREATE FUNCTION misty_agent_rehome_on_leave() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
DECLARE
  home text;
BEGIN
  -- An existing personal Space only: while an account is being deleted
  -- there is none to move to, and its agents go with it.
  SELECT id INTO home FROM spaces
    WHERE owner_user_id = OLD.user_id AND is_default AND lifecycle_state = 'active' AND id <> OLD.space_id;
  IF home IS NOT NULL THEN
    UPDATE misty_ask_identities SET space_id = home, updated_at = NOW()
      WHERE space_id = OLD.space_id AND owner_user_id = OLD.user_id;
  END IF;
  RETURN OLD;
END $$;

-- The personal Space is deleted only with its account, and the agents homed
-- there go with it (ON DELETE CASCADE).
CREATE FUNCTION misty_agent_rehome_on_space_delete() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
BEGIN
  UPDATE misty_ask_identities a SET space_id = s.id, updated_at = NOW()
    FROM spaces s
    WHERE a.space_id = OLD.id AND s.owner_user_id = a.owner_user_id AND s.is_default
      AND s.lifecycle_state = 'active' AND s.id <> OLD.id;
  RETURN OLD;
END $$;
-- +goose StatementEnd

CREATE TRIGGER misty_agent_home_space_guard BEFORE INSERT OR UPDATE OF space_id ON misty_ask_identities
    FOR EACH ROW EXECUTE FUNCTION misty_agent_home_space_guard();
DROP TRIGGER IF EXISTS misty_agent_space_membership ON misty_ask_identities;
CREATE TRIGGER misty_agent_space_membership AFTER INSERT OR UPDATE OF deleted_at, space_id ON misty_ask_identities
    FOR EACH ROW EXECUTE FUNCTION misty_agent_space_membership();
CREATE TRIGGER space_members_rehome_agents BEFORE DELETE ON space_members
    FOR EACH ROW EXECUTE FUNCTION misty_agent_rehome_on_leave();
CREATE TRIGGER spaces_rehome_agents BEFORE DELETE ON spaces
    FOR EACH ROW EXECUTE FUNCTION misty_agent_rehome_on_space_delete();

-- +goose Down
DROP TRIGGER IF EXISTS spaces_rehome_agents ON spaces;
DROP TRIGGER IF EXISTS space_members_rehome_agents ON space_members;
DROP TRIGGER IF EXISTS misty_agent_home_space_guard ON misty_ask_identities;
DROP TRIGGER IF EXISTS misty_agent_space_membership ON misty_ask_identities;
DROP FUNCTION IF EXISTS misty_agent_rehome_on_space_delete();
DROP FUNCTION IF EXISTS misty_agent_rehome_on_leave();
DROP FUNCTION IF EXISTS misty_agent_home_space_guard();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION misty_agent_space_membership() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
DECLARE
  home text;
BEGIN
  IF NEW.deleted_at IS NOT NULL THEN
    DELETE FROM space_agents WHERE agent_id=NEW.id;
    RETURN NULL;
  END IF;
  IF TG_OP='INSERT' THEN
    home := misty_ensure_personal_space(NEW.owner_user_id);
    IF home IS NOT NULL THEN
      INSERT INTO space_agents(space_id,agent_id,owner_user_id) VALUES(home,NEW.id,NEW.owner_user_id)
        ON CONFLICT DO NOTHING;
    END IF;
  END IF;
  RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER misty_agent_space_membership AFTER INSERT OR UPDATE OF deleted_at ON misty_ask_identities
    FOR EACH ROW EXECUTE FUNCTION misty_agent_space_membership();
DROP INDEX IF EXISTS misty_ask_identities_home_space;
ALTER TABLE misty_ask_identities DROP COLUMN space_id;
