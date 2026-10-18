-- +goose Up
-- Agents are members of Spaces under the person who owns them. A person can
-- bring their agent into any Space they belong to, and an agent can be in many
-- Spaces. Anyone in the Space can ask it for work; there are no listings,
-- accept policies or approvals. Membership needs its owner in the Space, so a
-- person's agents leave with them.
CREATE TABLE space_agents (
    space_id text NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    agent_id text NOT NULL REFERENCES misty_ask_identities(id) ON DELETE CASCADE,
    owner_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    added_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (space_id, agent_id),
    FOREIGN KEY (space_id, owner_user_id) REFERENCES space_members(space_id, user_id) ON DELETE CASCADE
);
CREATE INDEX space_agents_agent ON space_agents(agent_id);
CREATE INDEX space_agents_owner ON space_agents(owner_user_id);

-- Every live agent stays where it was, and is also in its owner's personal Space.
INSERT INTO space_agents(space_id,agent_id,owner_user_id)
    SELECT l.space_id,l.agent_id,l.owner_user_id FROM space_agent_listings l
    JOIN misty_ask_identities a ON a.id=l.agent_id AND a.owner_user_id=l.owner_user_id AND a.deleted_at IS NULL
    JOIN space_members m ON m.space_id=l.space_id AND m.user_id=l.owner_user_id
    ON CONFLICT DO NOTHING;
INSERT INTO space_agents(space_id,agent_id,owner_user_id)
    SELECT a.space_id,a.id,a.owner_user_id FROM misty_ask_identities a
    JOIN space_members m ON m.space_id=a.space_id AND m.user_id=a.owner_user_id
    WHERE a.deleted_at IS NULL AND a.space_id IS NOT NULL
    ON CONFLICT DO NOTHING;
INSERT INTO space_agents(space_id,agent_id,owner_user_id)
    SELECT s.id,a.id,a.owner_user_id FROM misty_ask_identities a
    JOIN spaces s ON s.owner_user_id=a.owner_user_id AND s.is_default AND s.lifecycle_state='active'
    WHERE a.deleted_at IS NULL
    ON CONFLICT DO NOTHING;

-- +goose StatementBegin
-- New agents start in their owner's personal Space; a deleted agent leaves
-- every Space.
CREATE FUNCTION misty_agent_space_membership() RETURNS trigger
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

ALTER TABLE space_agents ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_agents FORCE ROW LEVEL SECURITY;
CREATE POLICY space_agents_members ON space_agents
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service());
CREATE TRIGGER space_agents_changes AFTER INSERT OR UPDATE OR DELETE ON space_agents
    FOR EACH ROW EXECUTE FUNCTION misty_notify_account_change('agent_spaces', 'owner_user_id');

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'misty_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON space_agents TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- An agent no longer lives in exactly one Space.
DROP TRIGGER IF EXISTS spaces_rehome_agents_update ON spaces;
DROP TRIGGER IF EXISTS spaces_rehome_agents_delete ON spaces;
DROP TRIGGER IF EXISTS space_members_rehome_agents ON space_members;
DROP FUNCTION IF EXISTS misty_space_agents_leave();
DROP FUNCTION IF EXISTS misty_rehome_space_agents(text, text, boolean);
DROP TRIGGER IF EXISTS misty_agent_space_listing ON misty_ask_identities;
DROP FUNCTION IF EXISTS misty_agent_space_listing();
DROP TRIGGER IF EXISTS misty_agent_space_guard ON misty_ask_identities;
DROP FUNCTION IF EXISTS misty_agent_space_guard();
ALTER TABLE misty_ask_identities DROP CONSTRAINT IF EXISTS misty_ask_identities_space_required;
DROP INDEX IF EXISTS misty_ask_identities_space;
ALTER TABLE misty_ask_identities DROP COLUMN IF EXISTS space_id;
DROP TABLE space_agent_listings;

-- Asking someone else's agent starts the work at once; owners no longer approve.
DROP INDEX IF EXISTS agent_member_requests_pending;
UPDATE space_runs r SET state='canceled',error_code='approval_retired',
    error_message='Requests no longer wait for approval. Ask again.',completed_at=NOW(),updated_at=NOW()
    FROM agent_member_requests q WHERE q.child_run_id=r.id AND q.approval='pending' AND r.state='queued';
ALTER TABLE agent_member_requests DROP COLUMN approval, DROP COLUMN decided_at;

-- +goose Down
-- Retired membership rules are not restored.
SELECT 1;
