-- +goose Up
-- +goose StatementBegin
CREATE TABLE space_message_reactions (
    message_id TEXT NOT NULL REFERENCES space_messages(id) ON DELETE CASCADE,
    space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    emoji TEXT NOT NULL CHECK (char_length(emoji) BETWEEN 1 AND 8 AND octet_length(emoji) <= 32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (message_id,user_id,emoji)
);

CREATE INDEX space_message_reactions_message_idx ON space_message_reactions(message_id,created_at);
CREATE INDEX space_message_reactions_space_idx ON space_message_reactions(space_id,created_at DESC);

ALTER TABLE space_message_reactions ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_message_reactions FORCE ROW LEVEL SECURITY;
CREATE POLICY space_message_reactions_member_policy ON space_message_reactions FOR ALL
    USING (misty_rls_is_service() OR misty_is_space_member(space_id))
    WITH CHECK (misty_rls_is_service() OR misty_is_space_member(space_id));

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
        GRANT SELECT,INSERT,UPDATE,DELETE ON space_message_reactions TO misty_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS space_message_reactions;
-- +goose StatementEnd
