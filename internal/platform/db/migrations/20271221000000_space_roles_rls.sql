-- +goose Up
-- Private channels in row security. A conversation, its messages, their reactions, attachments
-- and Library references, its read marks, and everything scoped to it (notes, drawings, Library
-- items, calendar events, retrieval documents) are visible only to who can read the conversation
-- now. misty_can_read_space_conversation is that answer, the same one the API computes in Go
-- (channelPermissionsForTx): a member who can open the conversation (the whole Space is in it, or
-- they take part in it); the owner and administrators read every channel they can open; in a
-- standard channel the everyone role's overwrite applies first, then any deny of the member's other
-- roles, then any allow, which wins; a DM follows the member's Space-wide messages.read.
-- The API connects as the service (app.rls_mode = 'service'), which every policy lets through and
-- which checks the same rules itself; these policies hold for any connection that isn't.
SELECT set_config('app.rls_mode', 'service', true);

-- +goose StatementBegin
CREATE FUNCTION misty_can_read_space_conversation(candidate_conversation_id text, candidate_user_id text) RETURNS boolean
    LANGUAGE plpgsql STABLE SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
DECLARE
    conversation record;
    member_role text;
    everyone_role_id uuid;
    held_role_ids uuid[];
    is_administrator boolean;
    base_read boolean;
BEGIN
    IF candidate_conversation_id IS NULL OR candidate_user_id IS NULL THEN
        RETURN false;
    END IF;
    SELECT c.space_id, c.kind, c.visible_to_space INTO conversation
      FROM space_conversations c JOIN spaces s ON s.id = c.space_id AND s.lifecycle_state = 'active'
     WHERE c.id = candidate_conversation_id;
    IF NOT FOUND THEN
        RETURN false;
    END IF;
    SELECT sm.role INTO member_role FROM space_members sm
     WHERE sm.space_id = conversation.space_id AND sm.user_id = candidate_user_id;
    IF NOT FOUND THEN
        RETURN false;
    END IF;
    IF NOT conversation.visible_to_space AND NOT EXISTS (
        SELECT 1 FROM space_conversation_members cm
         WHERE cm.conversation_id = candidate_conversation_id AND cm.user_id = candidate_user_id) THEN
        RETURN false;
    END IF;
    IF member_role = 'owner' THEN
        RETURN true;
    END IF;
    SELECT r.id INTO everyone_role_id FROM space_roles r
     WHERE r.space_id = conversation.space_id AND r.is_default;
    -- The member's roles, everyone's included. A Space without an everyone role yet acts as if it
    -- had the default grant, which includes messages.read.
    SELECT COALESCE(array_agg(r.id), '{}'::uuid[]),
           COALESCE(bool_or('administrator' = ANY(r.permissions)), false),
           COALESCE(bool_or('messages.read' = ANY(r.permissions)), false) OR everyone_role_id IS NULL
      INTO held_role_ids, is_administrator, base_read
      FROM space_roles r
     WHERE r.space_id = conversation.space_id AND (r.is_default OR EXISTS (
         SELECT 1 FROM space_member_roles mr
          WHERE mr.space_id = r.space_id AND mr.user_id = candidate_user_id AND mr.role_id = r.id));
    IF is_administrator THEN
        RETURN true;
    END IF;
    IF conversation.kind <> 'standard' THEN
        RETURN base_read;
    END IF;
    IF EXISTS (SELECT 1 FROM space_conversation_overwrites o
                WHERE o.conversation_id = candidate_conversation_id AND o.role_id = ANY(held_role_ids)
                  AND o.role_id IS DISTINCT FROM everyone_role_id AND 'messages.read' = ANY(o.allow)) THEN
        RETURN true;
    END IF;
    IF EXISTS (SELECT 1 FROM space_conversation_overwrites o
                WHERE o.conversation_id = candidate_conversation_id AND o.role_id = ANY(held_role_ids)
                  AND o.role_id IS DISTINCT FROM everyone_role_id AND 'messages.read' = ANY(o.deny)) THEN
        RETURN false;
    END IF;
    IF EXISTS (SELECT 1 FROM space_conversation_overwrites o
                WHERE o.conversation_id = candidate_conversation_id AND o.role_id = everyone_role_id
                  AND 'messages.read' = ANY(o.allow)) THEN
        RETURN true;
    END IF;
    IF EXISTS (SELECT 1 FROM space_conversation_overwrites o
                WHERE o.conversation_id = candidate_conversation_id AND o.role_id = everyone_role_id
                  AND 'messages.read' = ANY(o.deny)) THEN
        RETURN false;
    END IF;
    RETURN base_read;
END $$;
-- +goose StatementEnd

-- A message is readable when it's in the Space-wide chat or in a conversation the viewer can read.
-- +goose StatementBegin
CREATE FUNCTION misty_can_read_space_message(candidate_message_id text) RETURNS boolean
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
    SELECT EXISTS (
        SELECT 1 FROM space_messages m
        WHERE m.id = candidate_message_id
          AND (m.conversation_id IS NULL OR misty_can_read_space_conversation(m.conversation_id, misty_rls_user_id()))
    )
$$;
-- +goose StatementEnd

-- Every policy that asked "is the viewer in this conversation" now asks whether they can read it:
-- space_messages, space_conversation_members and shared runs through this function, and every
-- conversation-scoped item through misty_can_access_space_audience below.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION misty_is_space_conversation_member(candidate_conversation_id text) RETURNS boolean
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
    SELECT misty_can_read_space_conversation(candidate_conversation_id, misty_rls_user_id())
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION misty_can_access_space_audience(candidate_space_id text, candidate_audience_kind text, candidate_conversation_id text) RETURNS boolean
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
    SELECT CASE
        WHEN candidate_audience_kind='space'
            THEN misty_is_space_member(candidate_space_id)
        WHEN candidate_audience_kind='conversation'
            THEN candidate_conversation_id IS NOT NULL
             AND EXISTS(SELECT 1 FROM space_conversations c
                        WHERE c.id=candidate_conversation_id AND c.space_id=candidate_space_id)
             AND misty_can_read_space_conversation(candidate_conversation_id, misty_rls_user_id())
        ELSE FALSE
    END
$$;
-- +goose StatementEnd

-- A channel's creator no longer sees it after losing read in it.
DROP POLICY space_conversations_read ON space_conversations;
CREATE POLICY space_conversations_read ON space_conversations FOR SELECT
    USING (misty_rls_is_service() OR misty_is_space_conversation_member(id));

DROP POLICY space_message_reactions_member_policy ON space_message_reactions;
CREATE POLICY space_message_reactions_member_policy ON space_message_reactions
    USING (misty_rls_is_service() OR (misty_is_space_member(space_id) AND misty_can_read_space_message(message_id)))
    WITH CHECK (misty_rls_is_service() OR (misty_is_space_member(space_id) AND misty_can_read_space_message(message_id)));

-- An attachment not yet posted has no message; it stays its Space's, as before.
DROP POLICY space_message_attachments_policy ON space_message_attachments;
CREATE POLICY space_message_attachments_policy ON space_message_attachments
    USING (misty_rls_is_service() OR (misty_is_space_member(space_id)
        AND (message_id IS NULL OR misty_can_read_space_message(message_id))))
    WITH CHECK (misty_rls_is_service() OR (misty_is_space_member(space_id)
        AND (message_id IS NULL OR misty_can_read_space_message(message_id))));

DROP POLICY message_library_references_policy ON space_message_library_references;
CREATE POLICY message_library_references_policy ON space_message_library_references
    USING (misty_rls_is_service() OR (misty_is_space_member(space_id) AND misty_can_read_space_message(message_id)))
    WITH CHECK (misty_rls_is_service() OR (misty_is_space_member(space_id) AND misty_can_read_space_message(message_id)));

DROP POLICY space_conversation_reads_policy ON space_conversation_reads;
CREATE POLICY space_conversation_reads_policy ON space_conversation_reads
    USING (misty_rls_is_service() OR (user_id = misty_rls_user_id() AND misty_is_space_conversation_member(conversation_id)))
    WITH CHECK (misty_rls_is_service() OR (user_id = misty_rls_user_id() AND misty_is_space_conversation_member(conversation_id)));

-- +goose Down
SELECT set_config('app.rls_mode', 'service', true);

DROP POLICY space_conversation_reads_policy ON space_conversation_reads;
CREATE POLICY space_conversation_reads_policy ON space_conversation_reads
    USING (misty_rls_is_service() OR user_id = misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR user_id = misty_rls_user_id());

DROP POLICY message_library_references_policy ON space_message_library_references;
CREATE POLICY message_library_references_policy ON space_message_library_references
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

DROP POLICY space_message_attachments_policy ON space_message_attachments;
CREATE POLICY space_message_attachments_policy ON space_message_attachments
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

DROP POLICY space_message_reactions_member_policy ON space_message_reactions;
CREATE POLICY space_message_reactions_member_policy ON space_message_reactions
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

DROP POLICY space_conversations_read ON space_conversations;
CREATE POLICY space_conversations_read ON space_conversations FOR SELECT
    USING (misty_rls_is_service() OR created_by_user_id = misty_rls_user_id() OR misty_is_space_conversation_member(id));

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION misty_can_access_space_audience(candidate_space_id text, candidate_audience_kind text, candidate_conversation_id text) RETURNS boolean
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
    SELECT CASE
        WHEN candidate_audience_kind='space'
            THEN misty_is_space_member(candidate_space_id)
        WHEN candidate_audience_kind='conversation'
            THEN candidate_conversation_id IS NOT NULL
             AND EXISTS(
                SELECT 1
                FROM space_conversation_members cm
                JOIN space_conversations c ON c.id=cm.conversation_id
                JOIN space_members sm ON sm.space_id=c.space_id AND sm.user_id=cm.user_id
                WHERE cm.conversation_id=candidate_conversation_id
                  AND c.space_id=candidate_space_id
                  AND cm.user_id=misty_rls_user_id()
             )
        ELSE FALSE
    END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION misty_is_space_conversation_member(candidate_conversation_id text) RETURNS boolean
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
    SELECT EXISTS (
        SELECT 1
        FROM space_conversations c
        JOIN space_members sm ON sm.space_id=c.space_id
        WHERE c.id=candidate_conversation_id
          AND sm.user_id=misty_rls_user_id()
          AND (
              c.visible_to_space OR EXISTS (
                  SELECT 1 FROM space_conversation_members cm
                  WHERE cm.conversation_id=c.id AND cm.user_id=misty_rls_user_id()
              )
          )
    )
$$;
-- +goose StatementEnd

DROP FUNCTION IF EXISTS misty_can_read_space_message(text);
DROP FUNCTION IF EXISTS misty_can_read_space_conversation(text, text);
