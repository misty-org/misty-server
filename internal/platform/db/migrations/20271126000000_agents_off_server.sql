-- +goose Up
-- Nothing AI runs on the server. Agents, their
-- runs, memory, workflows, schedules, chats and connected apps all live on the
-- person's computer now, so every table and function behind them goes. Spaces
-- keep what a person would have made by hand: notes, drawings, files, messages.
-- A Space lists an agent as a name under its person, with no row behind it.
SELECT set_config('app.rls_mode', 'service', true);

-- A Space's listing keeps the agent's name itself; the id is the computer's own.
ALTER TABLE space_agents ADD COLUMN agent_name text;
UPDATE space_agents m SET agent_name = left(a.name, 80) FROM misty_ask_identities a WHERE a.id = m.agent_id;
DELETE FROM space_agents WHERE COALESCE(agent_name, '') = '';
ALTER TABLE space_agents
    ALTER COLUMN agent_name SET NOT NULL,
    ADD CONSTRAINT space_agents_agent_name_check CHECK (char_length(agent_name) BETWEEN 1 AND 80),
    ADD CONSTRAINT space_agents_agent_id_check CHECK (char_length(agent_id) BETWEEN 1 AND 200),
    DROP CONSTRAINT space_agents_agent_id_fkey,
    DROP CONSTRAINT space_agents_pkey,
    ADD PRIMARY KEY (space_id, owner_user_id, agent_id);

-- Files an agent saved keep its id as provenance, with no row behind that either.
ALTER TABLE space_agent_artifacts DROP CONSTRAINT space_agent_artifacts_agent_id_fkey;
ALTER TABLE space_library_uploads DROP CONSTRAINT space_library_uploads_agent_id_fkey;

-- Chat is people only: an agent's reply is an ordinary message from its person.
DELETE FROM space_conversations WHERE kind = 'direct';
DELETE FROM space_conversation_members WHERE actor_kind <> 'person';
ALTER TABLE space_conversations
    DROP CONSTRAINT space_conversations_direct_actor_check,
    DROP CONSTRAINT space_conversations_kind_check,
    DROP COLUMN direct_user_id,
    DROP COLUMN direct_agent_id,
    ADD CONSTRAINT space_conversations_kind_check CHECK (kind IN ('standard', 'dm'));
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION misty_space_member_joins_group_chats() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    SET row_security TO 'off'
AS $$
BEGIN
  INSERT INTO space_conversation_members(conversation_id, user_id)
    SELECT c.id, NEW.user_id FROM space_conversations c
    WHERE c.space_id = NEW.space_id AND c.visible_to_space
    ON CONFLICT DO NOTHING;
  RETURN NULL;
END $$;

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
DROP INDEX IF EXISTS space_conversation_members_person_unique;
ALTER TABLE space_conversation_members
    DROP CONSTRAINT space_conversation_member_actor_check,
    DROP COLUMN agent_id,
    DROP COLUMN actor_kind,
    ALTER COLUMN user_id SET NOT NULL,
    ADD CONSTRAINT space_conversation_members_person_unique UNIQUE (conversation_id, user_id);
ALTER TABLE space_messages DROP COLUMN sender_agent_id, DROP COLUMN sender_kind, DROP COLUMN origin;

-- App actions were counted here; they run on the person's computer now.
ALTER TABLE account_controls DROP COLUMN app_actions_per_month;

DROP TRIGGER ai_index_space_notes ON space_notes;
DROP TRIGGER space_members_rehome_agents ON space_members;
DROP TRIGGER spaces_rehome_agents ON spaces;
DROP TRIGGER trusted_device_online_notify ON trusted_devices;

DROP TABLE
    a2a_push_configs,
    account_usage,
    agent_app_requests,
    agent_checklists,
    agent_conversation_folders,
    agent_delegation_children,
    agent_goals,
    agent_grants,
    agent_lesson_documents,
    agent_member_requests,
    agent_method_lessons,
    agent_method_versions,
    agent_methods,
    agent_model_turn_claims,
    agent_notebooks,
    agent_onboarding_steps,
    agent_plans,
    agent_question_sets,
    agent_run_contexts,
    agent_run_jobs,
    agent_runtime_deliveries,
    agent_runtime_start_receipts,
    agent_toolbox_action_journal,
    ai_artifacts,
    ai_cleanup_jobs,
    ai_conversation_attachments,
    ai_conversation_modes,
    ai_conversation_summaries,
    ai_feature_flags,
    ai_feedback,
    ai_intervention_waits,
    ai_invocation_contexts,
    ai_invocation_events,
    ai_invocations,
    ai_model_routes,
    ai_model_run_routes,
    ai_recaps,
    ai_retrieval_chunks,
    ai_retrieval_documents,
    ai_surface_preferences,
    ai_user_settings,
    browser_workflow_replays,
    composio_sessions,
    connected_account_oauth_states,
    connected_accounts,
    memory_backlog,
    memory_chunks,
    memory_events,
    misty_agent_execution_leases,
    misty_ask_conversation_events,
    misty_ask_conversations,
    misty_ask_identities,
    misty_ask_identity_versions,
    misty_memories,
    space_run_actions,
    space_run_steps,
    space_runs,
    workflow_device_node_jobs,
    workflow_schedules
    CASCADE;
ALTER TABLE cloud_connections DROP COLUMN connected_account_id;

DROP FUNCTION
    misty_agent_home_space_guard(),
    misty_agent_rehome_on_leave(),
    misty_agent_rehome_on_space_delete(),
    misty_agent_space_membership(),
    misty_ai_index_note(),
    misty_ai_index_provider_record(),
    misty_ai_write_index_document(text,text,text,text,text,text,text,text,text,text,jsonb),
    misty_cancel_run_device_work(),
    misty_conversation_space_guard(),
    misty_default_agent_run_owner(),
    misty_is_shared_space_run_visible(text,text,text),
    misty_memory_child_space(),
    misty_memory_chunk_put(text,text,text,text,text,timestamp with time zone),
    misty_memory_index_fact(),
    misty_memory_index_moment(),
    misty_memory_index_summary(),
    misty_memory_index_workflow(),
    misty_memory_moment_embedded(),
    misty_memory_space_guard(),
    misty_notify_device_job_state(),
    misty_notify_invocation_stream(),
    misty_notify_member_request_run(),
    misty_notify_resource_lease(),
    misty_notify_runtime_delivery(),
    misty_pause_agent_execution_clock(),
    misty_routine_agent_pin_immutable(),
    misty_routine_execution_immutable(),
    misty_routine_version_immutable(),
    misty_routine_wait_immutable(),
    preserve_agent_runtime_pin();

-- Nothing stores a vector any more. The image still ships the extension because
-- replaying the baseline migration creates it.
DROP EXTENSION IF EXISTS vector;

-- +goose Down
-- Agents, memory, Composio and their tables are not restored; their data was
-- removed in Up and now lives on each person's computer.
SELECT 1;
