-- +goose Up
-- Retire the SDK provider system, MCP connectors, Studio workflows, GitHub,
-- Figma, social messaging, provider OAuth and tool approvals. Nothing in the
-- server or runtime reads these tables or columns any more.

-- Work that was waiting on a retired feature cannot continue.
UPDATE space_runs
SET state = 'failed', error_code = 'retired_feature',
    error_message = 'This run used a feature Misty no longer supports.',
    completed_at = COALESCE(completed_at, NOW()), updated_at = NOW()
WHERE state = 'awaiting_approval'
   OR (state IN ('queued', 'running', 'awaiting_device', 'awaiting_intervention', 'cooldown', 'retrying')
       AND input ? '_misty_authority');

UPDATE ai_invocations
SET state = 'failed', error_code = 'retired_feature', updated_at = NOW()
WHERE state = 'awaiting_approval'
   OR (state IN ('queued', 'running', 'awaiting_device', 'awaiting_intervention', 'awaiting_timer')
       AND (surface_id IN ('sdk', 'routine') OR request_payload ? '_misty_authority'));

DELETE FROM agent_runtime_deliveries WHERE operation = 'approval.resume';

-- User-action continuations name their wait as wait_id.
UPDATE agent_runtime_deliveries
SET payload = (payload - 'approval_id') || jsonb_build_object('wait_id', payload -> 'approval_id')
WHERE operation = 'intervention.resume' AND payload ? 'approval_id';

ALTER TABLE agent_runtime_deliveries DROP CONSTRAINT agent_runtime_deliveries_operation_check;
ALTER TABLE agent_runtime_deliveries ADD CONSTRAINT agent_runtime_deliveries_operation_check
  CHECK (operation IN ('invocation.start', 'runtime.cancel', 'runtime.reconcile', 'device.resume', 'intervention.resume'));

ALTER TABLE ai_invocations DROP CONSTRAINT ai_invocations_state_check;
ALTER TABLE ai_invocations ADD CONSTRAINT ai_invocations_state_check
  CHECK (state IN ('queued', 'running', 'awaiting_device', 'awaiting_intervention', 'awaiting_timer', 'completed', 'failed', 'canceled'));

ALTER TABLE space_runs DROP CONSTRAINT space_runs_state_check;
ALTER TABLE space_runs ADD CONSTRAINT space_runs_state_check
  CHECK (state IN ('queued', 'running', 'awaiting_device', 'awaiting_intervention', 'completed', 'completed_with_errors', 'failed', 'canceled', 'rejected', 'cooldown', 'retrying'));

-- Dispatch hints no longer watch the approval wait.
DROP TRIGGER ai_invocation_dispatch_update_notify ON ai_invocations;
CREATE TRIGGER ai_invocation_dispatch_update_notify
AFTER UPDATE OF state, runtime_run_id, agent_run_id, device_wait_hook_token, device_wait_expires_at ON ai_invocations
FOR EACH ROW WHEN (
  OLD.state IS DISTINCT FROM NEW.state
  OR OLD.runtime_run_id IS DISTINCT FROM NEW.runtime_run_id
  OR OLD.agent_run_id IS DISTINCT FROM NEW.agent_run_id
  OR OLD.device_wait_hook_token IS DISTINCT FROM NEW.device_wait_hook_token
  OR OLD.device_wait_expires_at IS DISTINCT FROM NEW.device_wait_expires_at
) EXECUTE FUNCTION misty_notify_worker_queue('agent-runtime');

DROP TRIGGER space_run_dispatch_update_notify ON space_runs;
CREATE TRIGGER space_run_dispatch_update_notify
AFTER UPDATE OF state, runtime_phase, device_wait_hook_token, device_wait_expires_at, runtime_run_id, execution_owner ON space_runs
FOR EACH ROW WHEN (
  OLD.state IS DISTINCT FROM NEW.state
  OR OLD.runtime_phase IS DISTINCT FROM NEW.runtime_phase
  OR OLD.device_wait_hook_token IS DISTINCT FROM NEW.device_wait_hook_token
  OR OLD.device_wait_expires_at IS DISTINCT FROM NEW.device_wait_expires_at
  OR OLD.runtime_run_id IS DISTINCT FROM NEW.runtime_run_id
  OR OLD.execution_owner IS DISTINCT FROM NEW.execution_owner
) EXECUTE FUNCTION misty_notify_worker_queue('agent-tasks');

-- Recreate the policies that referenced provider sharing before its tables go.
-- The calendar branch now compares the event to the document's source; it
-- previously compared the event to itself and never matched.
DROP POLICY ai_retrieval_documents_read_policy ON ai_retrieval_documents;
CREATE POLICY ai_retrieval_documents_read_policy ON ai_retrieval_documents FOR SELECT USING (
  misty_rls_is_service() OR (
    lifecycle_state = 'active' AND (
      (privacy_class = 'private' AND owner_user_id = misty_rls_user_id())
      OR (privacy_class = 'shared' AND misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id))
      OR (privacy_class = 'provider'
          AND misty_can_access_space_audience(space_id, audience_kind, audience_conversation_id)
          AND source_kind = 'calendar'
          AND EXISTS (
            SELECT 1 FROM space_calendar_events e JOIN space_calendar_sources s ON s.id = e.source_id
            WHERE e.id = ai_retrieval_documents.source_id AND e.removed_at IS NULL AND s.status = 'active'))
    )
  )
);

DROP POLICY space_provider_credentials_owner ON space_provider_credentials;
CREATE POLICY space_provider_credentials_owner ON space_provider_credentials
USING (
  misty_rls_is_service() OR user_id = misty_rls_user_id()
  OR EXISTS (SELECT 1 FROM spaces s WHERE s.id = space_provider_credentials.space_id AND s.owner_user_id = misty_rls_user_id())
)
WITH CHECK (
  misty_rls_is_service() OR user_id = misty_rls_user_id()
  OR EXISTS (SELECT 1 FROM spaces s WHERE s.id = space_provider_credentials.space_id AND s.owner_user_id = misty_rls_user_id())
);

ALTER TABLE space_runs
  DROP COLUMN approval_wait_id,
  DROP COLUMN approval_state,
  DROP COLUMN workflow_identifier,
  DROP COLUMN workflow_version_id,
  DROP COLUMN workflow_version,
  DROP COLUMN capability_id,
  DROP COLUMN action_envelope;
ALTER TABLE ai_invocations DROP COLUMN approval_wait_id;
ALTER TABLE space_messages DROP COLUMN social_identity_id;

DROP TABLE
  agent_run_tool_approvals,
  agent_sdk_capability_bindings,
  cloud_credential_handoffs,
  cloud_oauth_states,
  figma_comment_audit,
  figma_content_records,
  figma_space_bindings,
  figma_webhook_deliveries,
  figma_webhook_subscriptions,
  github_app_installations,
  github_app_setup_states,
  github_code_workspaces,
  github_credential_handoffs,
  github_mutation_audit,
  github_repository_records,
  github_webhook_deliveries,
  mcp_discovery_snapshots,
  mcp_oauth_credentials,
  mcp_oauth_states,
  mcp_remote_connections,
  mcp_remote_tools,
  mcp_tool_execution_audit,
  misty_ask_mcp_tools,
  provider_content_records,
  provider_event_inbox,
  provider_gateway_state,
  provider_oauth_states,
  provider_shared_resources,
  provider_subscriptions,
  sdk_backend_connection_versions,
  sdk_backend_connections,
  sdk_capability_invocations,
  sdk_provider_registrations,
  sdk_provider_versions,
  sdk_target_versions,
  sdk_targets,
  social_automation_rules,
  social_automation_runs,
  social_bindings,
  social_identities,
  social_outbound_commands,
  social_scheduled_messages,
  social_send_authorities,
  space_device_presence,
  space_run_approvals,
  space_workflow_action_journal,
  space_workflow_resource_leases,
  space_workflow_versions,
  space_workflows;

-- +goose Down
-- Retired features are not restored; their data was removed in Up.
SELECT 1;
