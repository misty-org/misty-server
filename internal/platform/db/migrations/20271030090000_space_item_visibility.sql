-- +goose Up
-- Private and public layers (docs/spaces/as-profiles.md#private-and-public).
-- Every Space item has a private/public toggle: notes, drawings, Library files
-- and images, folders and links, code projects, and agents in a Space. A
-- private item stays in the Space but only its owner sees it. New items start
-- public in a shared Space and private in a Space only their owner belongs to.
-- Channels, threads and the Space's name, color and members have no toggle.

-- +goose StatementBegin
-- True while the Space has exactly one member.
CREATE FUNCTION misty_space_is_solo(candidate_space_id text) RETURNS boolean
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
    SELECT (SELECT count(*) FROM space_members WHERE space_id = candidate_space_id) = 1
$$;

-- A new item without a visibility takes the Space's default. An explicit
-- value is kept.
CREATE FUNCTION misty_item_default_visibility() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
AS $$
BEGIN
  IF NEW.visibility IS NULL THEN
    NEW.visibility := CASE WHEN misty_space_is_solo(NEW.space_id) THEN 'private' ELSE 'public' END;
  END IF;
  RETURN NEW;
END $$;

-- Only the item's owner flips its layer. TG_ARGV[0] names the owner column.
CREATE FUNCTION misty_item_visibility_guard() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
AS $$
BEGIN
  IF NEW.visibility IS DISTINCT FROM OLD.visibility AND NOT misty_rls_is_service()
     AND (to_jsonb(OLD) ->> TG_ARGV[0]) IS DISTINCT FROM misty_rls_user_id() THEN
    RAISE EXCEPTION 'only the owner changes an item''s visibility' USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd

-- One block per table: the column, its backfill, the triggers.
ALTER TABLE space_notes ADD COLUMN visibility text NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'private'));
ALTER TABLE space_drawings ADD COLUMN visibility text NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'private'));
ALTER TABLE space_library_items ADD COLUMN visibility text NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'private'));
ALTER TABLE space_nodes ADD COLUMN visibility text NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'private'));
ALTER TABLE space_code_projects ADD COLUMN visibility text NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'private'));
ALTER TABLE space_agents ADD COLUMN visibility text NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'private'));

UPDATE space_notes SET visibility = 'private' WHERE misty_space_is_solo(space_id);
UPDATE space_drawings SET visibility = 'private' WHERE misty_space_is_solo(space_id);
UPDATE space_library_items SET visibility = 'private' WHERE misty_space_is_solo(space_id);
UPDATE space_nodes SET visibility = 'private' WHERE misty_space_is_solo(space_id);
UPDATE space_code_projects SET visibility = 'private' WHERE misty_space_is_solo(space_id);
UPDATE space_agents SET visibility = 'private' WHERE misty_space_is_solo(space_id);

-- New rows get the Space's default from the trigger, not a column default.
ALTER TABLE space_notes ALTER COLUMN visibility DROP DEFAULT;
ALTER TABLE space_drawings ALTER COLUMN visibility DROP DEFAULT;
ALTER TABLE space_library_items ALTER COLUMN visibility DROP DEFAULT;
ALTER TABLE space_nodes ALTER COLUMN visibility DROP DEFAULT;
ALTER TABLE space_code_projects ALTER COLUMN visibility DROP DEFAULT;
ALTER TABLE space_agents ALTER COLUMN visibility DROP DEFAULT;

CREATE TRIGGER space_notes_default_visibility BEFORE INSERT ON space_notes FOR EACH ROW EXECUTE FUNCTION misty_item_default_visibility();
CREATE TRIGGER space_drawings_default_visibility BEFORE INSERT ON space_drawings FOR EACH ROW EXECUTE FUNCTION misty_item_default_visibility();
CREATE TRIGGER space_library_items_default_visibility BEFORE INSERT ON space_library_items FOR EACH ROW EXECUTE FUNCTION misty_item_default_visibility();
CREATE TRIGGER space_nodes_default_visibility BEFORE INSERT ON space_nodes FOR EACH ROW EXECUTE FUNCTION misty_item_default_visibility();
CREATE TRIGGER space_code_projects_default_visibility BEFORE INSERT ON space_code_projects FOR EACH ROW EXECUTE FUNCTION misty_item_default_visibility();
CREATE TRIGGER space_agents_default_visibility BEFORE INSERT ON space_agents FOR EACH ROW EXECUTE FUNCTION misty_item_default_visibility();

CREATE TRIGGER space_notes_visibility_guard BEFORE UPDATE OF visibility ON space_notes FOR EACH ROW EXECUTE FUNCTION misty_item_visibility_guard('creator_user_id');
CREATE TRIGGER space_drawings_visibility_guard BEFORE UPDATE OF visibility ON space_drawings FOR EACH ROW EXECUTE FUNCTION misty_item_visibility_guard('creator_user_id');
CREATE TRIGGER space_library_items_visibility_guard BEFORE UPDATE OF visibility ON space_library_items FOR EACH ROW EXECUTE FUNCTION misty_item_visibility_guard('added_by_user_id');
CREATE TRIGGER space_nodes_visibility_guard BEFORE UPDATE OF visibility ON space_nodes FOR EACH ROW EXECUTE FUNCTION misty_item_visibility_guard('uploader_user_id');
CREATE TRIGGER space_code_projects_visibility_guard BEFORE UPDATE OF visibility ON space_code_projects FOR EACH ROW EXECUTE FUNCTION misty_item_visibility_guard('owner_user_id');
CREATE TRIGGER space_agents_visibility_guard BEFORE UPDATE OF visibility ON space_agents FOR EACH ROW EXECUTE FUNCTION misty_item_visibility_guard('owner_user_id');

CREATE INDEX space_notes_private_owner ON space_notes(space_id, creator_user_id) WHERE visibility = 'private';
CREATE INDEX space_library_items_private_owner ON space_library_items(space_id, added_by_user_id) WHERE visibility = 'private';

-- The policies: public, or owned by the viewer. A forgotten filter in a
-- query still can't leak a private item to another member.
DROP POLICY space_notes_audience_policy ON space_notes;
CREATE POLICY space_notes_audience_policy ON space_notes
    USING (misty_rls_is_service() OR (lifecycle_state = 'active'
        AND misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)
        AND (visibility = 'public' OR creator_user_id = misty_rls_user_id())))
    WITH CHECK (misty_rls_is_service() OR (creator_user_id = misty_rls_user_id()
        AND misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)));

DROP POLICY space_drawings_audience_policy ON space_drawings;
CREATE POLICY space_drawings_audience_policy ON space_drawings
    USING (misty_rls_is_service() OR (lifecycle_state = 'active'
        AND misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)
        AND (visibility = 'public' OR creator_user_id = misty_rls_user_id())))
    WITH CHECK (misty_rls_is_service() OR (creator_user_id = misty_rls_user_id()
        AND misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)));

DROP POLICY space_library_items_audience_policy ON space_library_items;
CREATE POLICY space_library_items_audience_policy ON space_library_items
    USING (misty_rls_is_service() OR (misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)
        AND (visibility = 'public' OR added_by_user_id = misty_rls_user_id())))
    WITH CHECK (misty_rls_is_service() OR (misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)
        AND (visibility = 'public' OR added_by_user_id = misty_rls_user_id())));

DROP POLICY space_nodes_member_policy ON space_nodes;
CREATE POLICY space_nodes_member_policy ON space_nodes
    USING (misty_rls_is_service() OR (misty_is_space_member(space_id)
        AND (visibility = 'public' OR uploader_user_id = misty_rls_user_id())))
    WITH CHECK (misty_rls_is_service() OR (misty_is_space_member(space_id)
        AND (visibility = 'public' OR uploader_user_id = misty_rls_user_id())));

DROP POLICY space_code_projects_members ON space_code_projects;
CREATE POLICY space_code_projects_members ON space_code_projects
    USING (misty_rls_is_service() OR (misty_is_space_member(space_id)
        AND (visibility = 'public' OR owner_user_id = misty_rls_user_id())))
    WITH CHECK (misty_rls_is_service());

-- A private agent is invisible to the rest of the Space.
DROP POLICY space_agents_members ON space_agents;
CREATE POLICY space_agents_members ON space_agents
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id()
        OR (visibility = 'public' AND misty_is_space_member(space_id)))
    WITH CHECK (misty_rls_is_service());

-- +goose Down
DROP POLICY space_agents_members ON space_agents;
CREATE POLICY space_agents_members ON space_agents
    USING (misty_rls_is_service() OR owner_user_id = misty_rls_user_id() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service());
DROP POLICY space_code_projects_members ON space_code_projects;
CREATE POLICY space_code_projects_members ON space_code_projects
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service());
DROP POLICY space_nodes_member_policy ON space_nodes;
CREATE POLICY space_nodes_member_policy ON space_nodes
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));
DROP POLICY space_library_items_audience_policy ON space_library_items;
CREATE POLICY space_library_items_audience_policy ON space_library_items
    USING (misty_rls_is_service() OR misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id))
    WITH CHECK (misty_rls_is_service() OR misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id));
DROP POLICY space_drawings_audience_policy ON space_drawings;
CREATE POLICY space_drawings_audience_policy ON space_drawings
    USING (misty_rls_is_service() OR ((lifecycle_state = 'active') AND misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)))
    WITH CHECK (misty_rls_is_service() OR ((creator_user_id = misty_rls_user_id()) AND misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)));
DROP POLICY space_notes_audience_policy ON space_notes;
CREATE POLICY space_notes_audience_policy ON space_notes
    USING (misty_rls_is_service() OR ((lifecycle_state = 'active') AND misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)))
    WITH CHECK (misty_rls_is_service() OR ((creator_user_id = misty_rls_user_id()) AND misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)));
DROP TRIGGER IF EXISTS space_notes_visibility_guard ON space_notes;
DROP TRIGGER IF EXISTS space_notes_default_visibility ON space_notes;
DROP TRIGGER IF EXISTS space_drawings_visibility_guard ON space_drawings;
DROP TRIGGER IF EXISTS space_drawings_default_visibility ON space_drawings;
DROP TRIGGER IF EXISTS space_library_items_visibility_guard ON space_library_items;
DROP TRIGGER IF EXISTS space_library_items_default_visibility ON space_library_items;
DROP TRIGGER IF EXISTS space_nodes_visibility_guard ON space_nodes;
DROP TRIGGER IF EXISTS space_nodes_default_visibility ON space_nodes;
DROP TRIGGER IF EXISTS space_code_projects_visibility_guard ON space_code_projects;
DROP TRIGGER IF EXISTS space_code_projects_default_visibility ON space_code_projects;
DROP TRIGGER IF EXISTS space_agents_visibility_guard ON space_agents;
DROP TRIGGER IF EXISTS space_agents_default_visibility ON space_agents;
DROP INDEX IF EXISTS space_library_items_private_owner;
DROP INDEX IF EXISTS space_notes_private_owner;
ALTER TABLE space_agents DROP COLUMN visibility;
ALTER TABLE space_code_projects DROP COLUMN visibility;
ALTER TABLE space_nodes DROP COLUMN visibility;
ALTER TABLE space_library_items DROP COLUMN visibility;
ALTER TABLE space_drawings DROP COLUMN visibility;
ALTER TABLE space_notes DROP COLUMN visibility;
DROP FUNCTION IF EXISTS misty_item_visibility_guard();
DROP FUNCTION IF EXISTS misty_item_default_visibility();
DROP FUNCTION IF EXISTS misty_space_is_solo(text);
