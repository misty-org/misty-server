-- +goose Up
-- Conversations and messages imported from Discord, Instagram and Slack belong
-- to the retired social messaging feature. Items scoped only to an imported
-- conversation are removed with it by the existing ON DELETE CASCADE keys.
DELETE FROM space_conversations WHERE origin <> 'misty';
DELETE FROM space_messages
WHERE social_provider IS NOT NULL
   OR origin ->> 'system' IN ('discord', 'instagram', 'slack');

DROP POLICY space_conversations_disconnected_discord_owner_delete ON space_conversations;
ALTER TABLE space_conversations
  DROP COLUMN origin,
  DROP COLUMN integration_id,
  DROP COLUMN external_resource_id,
  DROP COLUMN external_display_name,
  DROP COLUMN integration_status;
ALTER TABLE space_messages DROP COLUMN social_provider;

-- Object cleanup: nothing ever processed these jobs, so the queue only grew.
DROP TRIGGER ai_attachment_removed_objects ON ai_conversation_attachments;
DROP TRIGGER ai_attachment_replaced_objects ON ai_conversation_attachments;
DROP TRIGGER user_avatar_deleted ON users;
DROP TRIGGER user_avatar_replaced ON users;
DROP FUNCTION queue_removed_ai_attachment_objects();
DROP FUNCTION queue_replaced_user_avatar();
DROP TABLE object_deletion_jobs;

-- +goose Down
-- Imported conversations and the unused object-cleanup queue are not restored.
SELECT 1;
