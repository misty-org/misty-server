-- +goose Up
-- Everyone in a Space is a member of its public channels. People who join later are added by
-- misty_space_member_joins_group_chats; members from before that trigger were never added, so they
-- could read and post in a public channel but got no live updates and couldn't be @mentioned there.
INSERT INTO space_conversation_members(conversation_id, user_id)
    SELECT c.id, m.user_id
    FROM space_conversations c
    JOIN space_members m ON m.space_id = c.space_id
    WHERE c.visible_to_space
    ON CONFLICT DO NOTHING;

-- +goose Down
-- The rows are what membership should have been; nothing to undo.
SELECT 1;
