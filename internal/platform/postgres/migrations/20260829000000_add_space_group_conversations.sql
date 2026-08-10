-- +goose Up
-- +goose StatementBegin
CREATE TABLE space_conversations (
    id TEXT PRIMARY KEY,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 80),
    created_by_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX space_conversations_space_idx ON space_conversations(space_id,updated_at DESC);

CREATE TABLE space_conversation_members (
    conversation_id TEXT NOT NULL REFERENCES space_conversations(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (conversation_id,user_id)
);

CREATE INDEX space_conversation_members_user_idx ON space_conversation_members(user_id,conversation_id);

ALTER TABLE space_messages
    ADD COLUMN conversation_id TEXT REFERENCES space_conversations(id) ON DELETE CASCADE;

DROP INDEX space_messages_history_idx;
CREATE INDEX space_messages_history_idx ON space_messages(space_id,conversation_id,seq DESC);

CREATE OR REPLACE FUNCTION misty_is_space_conversation_member(candidate_conversation_id TEXT)
RETURNS BOOLEAN
LANGUAGE SQL
STABLE
SECURITY DEFINER
SET search_path = public, pg_temp
SET row_security = off
AS $$
    SELECT EXISTS (
        SELECT 1
        FROM space_conversation_members cm
        JOIN space_conversations c ON c.id=cm.conversation_id
        JOIN space_members sm ON sm.space_id=c.space_id AND sm.user_id=cm.user_id
        WHERE cm.conversation_id=candidate_conversation_id
          AND cm.user_id=misty_rls_user_id()
    )
$$;

ALTER TABLE space_conversations ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_conversations FORCE ROW LEVEL SECURITY;
CREATE POLICY space_conversations_read ON space_conversations FOR SELECT
    USING (misty_rls_is_service() OR created_by_user_id=misty_rls_user_id() OR misty_is_space_conversation_member(id));
CREATE POLICY space_conversations_create ON space_conversations FOR INSERT
    WITH CHECK (misty_rls_is_service() OR (created_by_user_id=misty_rls_user_id() AND misty_is_space_member(space_id)));
CREATE POLICY space_conversations_creator_write ON space_conversations FOR UPDATE
    USING (misty_rls_is_service() OR created_by_user_id=misty_rls_user_id())
    WITH CHECK (misty_rls_is_service() OR created_by_user_id=misty_rls_user_id());
CREATE POLICY space_conversations_creator_delete ON space_conversations FOR DELETE
    USING (misty_rls_is_service() OR created_by_user_id=misty_rls_user_id());

ALTER TABLE space_conversation_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_conversation_members FORCE ROW LEVEL SECURITY;
CREATE POLICY space_conversation_members_read ON space_conversation_members FOR SELECT
    USING (misty_rls_is_service() OR misty_is_space_conversation_member(conversation_id));
CREATE POLICY space_conversation_members_creator_write ON space_conversation_members FOR ALL
    USING (misty_rls_is_service() OR EXISTS(
        SELECT 1 FROM space_conversations c
        WHERE c.id=conversation_id AND c.created_by_user_id=misty_rls_user_id()
    ))
    WITH CHECK (misty_rls_is_service() OR EXISTS(
        SELECT 1 FROM space_conversations c
        WHERE c.id=conversation_id AND c.created_by_user_id=misty_rls_user_id()
    ));

DROP POLICY space_messages_member_policy ON space_messages;
CREATE POLICY space_messages_conversation_policy ON space_messages FOR ALL
    USING (
        misty_rls_is_service() OR
        (conversation_id IS NULL AND misty_is_space_member(space_id)) OR
        (conversation_id IS NOT NULL AND misty_is_space_conversation_member(conversation_id))
    )
    WITH CHECK (
        misty_rls_is_service() OR
        (conversation_id IS NULL AND misty_is_space_member(space_id)) OR
        (conversation_id IS NOT NULL AND misty_is_space_conversation_member(conversation_id))
    );

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_conversations,space_conversation_members TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS space_messages_conversation_policy ON space_messages;
CREATE POLICY space_messages_member_policy ON space_messages FOR ALL
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

DROP FUNCTION IF EXISTS misty_is_space_conversation_member(TEXT);
DROP INDEX IF EXISTS space_messages_history_idx;
ALTER TABLE space_messages DROP COLUMN IF EXISTS conversation_id;
CREATE INDEX space_messages_history_idx ON space_messages(space_id,seq DESC);
DROP TABLE IF EXISTS space_conversation_members;
DROP TABLE IF EXISTS space_conversations;
-- +goose StatementEnd
