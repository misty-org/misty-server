-- +goose Up
-- Classify before touching any shared history. A NULL personal_agent_id is
-- global Ask (including old companion-mode Ask conversations), not a retired
-- agent. System-managed identities and their version-pinned runs survive.
CREATE TEMP TABLE retired_ask_agent_ids ON COMMIT DROP AS
SELECT id FROM personal_agents WHERE NOT system_managed
UNION SELECT id FROM space_agents;
CREATE TEMP TABLE retired_ask_conversation_ids ON COMMIT DROP AS
SELECT id FROM agent_conversations
WHERE personal_agent_id IN (SELECT id FROM retired_ask_agent_ids);
CREATE TEMP TABLE retired_ask_run_ids ON COMMIT DROP AS
SELECT id FROM space_runs WHERE agent_id IN (SELECT id FROM retired_ask_agent_ids)
 OR (resource_kind='agent' AND NOT EXISTS (
   SELECT 1 FROM personal_agents a WHERE a.id=space_runs.agent_id AND a.system_managed
 ) AND COALESCE(agent_version_snapshot->>'system_managed','false') <> 'true');

-- Use the existing durable object-cleanup queue for retired uploaded avatars.
-- Never queue a blob still referenced by a user or a surviving Ask identity/version.
INSERT INTO object_deletion_jobs(object_key,not_before,created_by_user_id)
SELECT DISTINCT 'avatars/' || retired.asset_id,NOW(),retired.owner_user_id
FROM (
 SELECT avatar->>'asset_id' AS asset_id,owner_user_id FROM personal_agents WHERE NOT system_managed AND avatar->>'kind'='upload'
 UNION SELECT v.avatar->>'asset_id',a.owner_user_id FROM personal_agent_versions v JOIN personal_agents a ON a.id=v.agent_id
 WHERE NOT a.system_managed AND v.avatar->>'kind'='upload'
) retired
WHERE retired.asset_id ~ '^avatar_[0-9a-f-]{36}$'
 AND NOT EXISTS (SELECT 1 FROM users WHERE avatar_object_key='avatars/' || retired.asset_id)
 AND NOT EXISTS (SELECT 1 FROM personal_agents WHERE system_managed AND avatar->>'asset_id'=retired.asset_id)
 AND NOT EXISTS (SELECT 1 FROM personal_agent_versions v JOIN personal_agents a ON a.id=v.agent_id WHERE a.system_managed AND v.avatar->>'asset_id'=retired.asset_id)
ON CONFLICT(object_key) DO NOTHING;

-- Purge non-cascading history before foreign keys lose their original identity.
DELETE FROM space_task_activity WHERE actor_agent_id IN (SELECT id FROM retired_ask_agent_ids)
 OR run_id IN (SELECT id FROM retired_ask_run_ids)
 OR metadata->>'agent_id' IN (SELECT id FROM retired_ask_agent_ids);
DELETE FROM space_inbox_items WHERE payload->>'agent_id' IN (SELECT id FROM retired_ask_agent_ids)
 OR payload->>'run_id' IN (SELECT id FROM retired_ask_run_ids)
 OR event_id IN (SELECT id FROM space_events WHERE entity_id IN (SELECT id FROM retired_ask_agent_ids)
  OR entity_id IN (SELECT id FROM retired_ask_run_ids)
  OR event_type LIKE 'action_suggestion.%' OR event_type LIKE 'conversation_follow_up.%');
DELETE FROM space_events WHERE entity_id IN (SELECT id FROM retired_ask_agent_ids)
 OR entity_id IN (SELECT id FROM retired_ask_run_ids)
 OR payload->>'agent_id' IN (SELECT id FROM retired_ask_agent_ids)
 OR payload->>'run_id' IN (SELECT id FROM retired_ask_run_ids)
 OR event_type LIKE 'action_suggestion.%' OR event_type LIKE 'conversation_follow_up.%';

CREATE TEMP TABLE retired_ask_invocation_ids ON COMMIT DROP AS
SELECT id FROM ai_invocations WHERE conversation_id IN (SELECT id FROM retired_ask_conversation_ids)
 OR agent_run_id IN (SELECT id FROM retired_ask_run_ids);
DELETE FROM agent_runtime_deliveries WHERE run_id IN (SELECT id FROM retired_ask_run_ids)
 OR run_id IN (SELECT id FROM retired_ask_invocation_ids);
DELETE FROM agent_runtime_start_receipts WHERE run_id IN (SELECT id FROM retired_ask_run_ids)
 OR run_id IN (SELECT id FROM retired_ask_invocation_ids);
DELETE FROM agent_toolbox_action_journal WHERE agent_id IN (SELECT id FROM retired_ask_agent_ids)
 OR run_id IN (SELECT id FROM retired_ask_run_ids) OR run_id IN (SELECT id FROM retired_ask_invocation_ids)
 OR session_id IN (SELECT id FROM retired_ask_conversation_ids);

-- Invocation/event/effect rows cascade from their execution roots. Unrelated
-- Ask invocations, model receipts and connected accounts are not purged.
DELETE FROM ai_invocations WHERE conversation_id IN (SELECT id FROM retired_ask_conversation_ids)
 OR agent_run_id IN (SELECT id FROM retired_ask_run_ids);
DELETE FROM space_runs WHERE id IN (SELECT id FROM retired_ask_run_ids);
DELETE FROM agent_conversations WHERE id IN (SELECT id FROM retired_ask_conversation_ids);
DELETE FROM space_conversation_follow_ups WHERE agent_id IN (SELECT id FROM retired_ask_agent_ids);
DELETE FROM space_messages WHERE sender_agent_id IN (SELECT id FROM retired_ask_agent_ids);

-- Preserve ordinary Space content and its original private audience. Retain
-- private conversation containers as human-only saved work, purge their retired
-- assistant history, and never broaden task or document access to the Space.
DELETE FROM space_messages WHERE conversation_id IN (
 SELECT id FROM space_conversations WHERE direct_agent_id IN (SELECT id FROM retired_ask_agent_ids)
);
UPDATE space_conversations SET kind='standard',direct_user_id=NULL,direct_agent_id=NULL,title='Saved work'
WHERE direct_agent_id IS NOT NULL;
DELETE FROM space_conversation_members WHERE agent_id IS NOT NULL;
UPDATE space_tasks t SET created_by_user_id=COALESCE(t.created_by_user_id,s.owner_user_id),created_by_agent_id=NULL
FROM spaces s WHERE s.id=t.space_id AND t.created_by_agent_id IN (SELECT id FROM retired_ask_agent_ids);
UPDATE agent_conversations SET personal_agent_id=NULL,conversation_kind='misty'
WHERE personal_agent_id IN (SELECT id FROM personal_agents WHERE system_managed);
DELETE FROM personal_agents WHERE NOT system_managed;
DELETE FROM space_agent_message_triggers;
UPDATE space_agents SET published_agent_version_id=NULL;
DELETE FROM space_agent_instances;
DELETE FROM space_agents;

-- Dedicated singleton storage; ALTER preserves IDs, foreign keys, RLS, grants,
-- MCP bindings, run approvals, cancellation and durable execution references.
ALTER TABLE personal_agents RENAME TO misty_ask_identities;
ALTER TABLE personal_agent_versions RENAME TO misty_ask_identity_versions;
ALTER TABLE personal_agent_mcp_tools RENAME TO misty_ask_mcp_tools;
ALTER TABLE agent_conversations RENAME TO misty_ask_conversations;
ALTER TABLE agent_conversation_events RENAME TO misty_ask_conversation_events;
ALTER TABLE misty_ask_identities ALTER COLUMN system_managed SET DEFAULT TRUE;
ALTER TABLE misty_ask_identities ADD CONSTRAINT misty_ask_identity_only CHECK (system_managed);
ALTER TABLE misty_ask_identities DROP COLUMN source_space_agent_id;
ALTER TABLE misty_ask_conversations DROP COLUMN personal_agent_id;
UPDATE misty_ask_conversations SET conversation_kind='misty';

-- Remove the retired ownership, configuration and autonomous-trigger schemas.
-- CASCADE removes only dependencies on these retired tables (principally old
-- instance/version foreign keys on preserved execution rows).
DROP TABLE space_agent_conversation_events, space_agent_conversations,
 space_agent_instance_workflows, space_agent_memory_events, space_workflow_event_claims,
 space_agent_message_triggers, space_agent_version_workflows, space_agent_instances,
 space_agent_versions, space_agents CASCADE;
DROP TABLE space_action_suggestion_dismissals, space_action_suggestion_items,
 space_action_suggestion_jobs, space_action_suggestion_batches, space_action_suggestion_settings,
 space_conversation_follow_up_recipients, space_conversation_follow_ups, space_conversation_suggestion_vetoes CASCADE;
ALTER TABLE ai_user_settings DROP COLUMN active_companion_agent_id;
ALTER TABLE ai_surface_preferences DROP COLUMN pinned_agent_id;

DELETE FROM space_member_permission_overrides WHERE permission='agents.manage';
UPDATE space_member_permission_overrides SET permission='ask.run' WHERE permission='agents.run';
UPDATE space_roles SET permissions=(
 SELECT COALESCE(jsonb_agg(CASE WHEN value='"agents.run"'::jsonb THEN '"ask.run"'::jsonb ELSE value END),'[]'::jsonb)
 FROM jsonb_array_elements(permissions) WHERE value <> '"agents.manage"'::jsonb
);

-- +goose Down
-- Deleted custom-agent histories cannot be reconstructed. Roll forward.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Global Ask retirement is irreversible; restore a pre-migration backup to roll back'; END $$;
-- +goose StatementEnd
