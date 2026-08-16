package db

import (
	"testing"
)

func TestTablesHaveRowLevelSecurityEnabled(t *testing.T) {
	database := openTestDatabase(t)

	tables := []string{
		"users",
		"licenses",
		"sessions",
		"password_reset_tokens",
		"waitlist_signups",
		"stripe_purchases",
		"stripe_subscriptions",
		"stripe_webhook_events",
		"hosted_ai_wallets",
		"hosted_ai_reservations",
		"hosted_ai_usage_ledger",
		"owner_storage_usage",
		"smart_library_folders",
		"smart_library_assets",
		"smart_library_batches",
		"smart_library_cost_events",
		"media_search_devices",
		"media_search_assets",
		"media_search_chunks",
		"media_search_segments",
		"trusted_devices",
		"trusted_device_request_nonces",
		"spaces",
		"space_members",
		"space_invitations",
		"space_messages",
		"space_message_reactions",
		"space_conversations",
		"space_conversation_members",
		"space_nodes",
		"space_agents",
		"space_workflows",
		"space_workflow_versions",
		"space_integrations",
		"space_runs",
		"space_run_actions",
		"space_run_approvals",
		"space_agent_conversations",
		"space_agent_conversation_events",
		"space_agent_versions",
		"space_agent_version_workflows",
		"space_agent_instances",
		"space_agent_instance_workflows",
		"space_agent_memory_events",
		"space_run_steps",
		"space_workflow_event_claims",
		"space_workflow_resource_leases",
		"space_workflow_action_journal",
		"space_provider_credentials",
		"provider_oauth_states",
		"provider_subscriptions",
		"provider_event_inbox",
		"connected_accounts",
		"connected_account_oauth_states",
		"cloud_connections",
		"cloud_oauth_states",
		"cloud_credential_handoffs",
		"mail_action_audit",
		"github_app_setup_states",
		"github_app_installations",
		"github_code_workspaces",
		"github_repository_records",
		"github_webhook_deliveries",
		"github_credential_handoffs",
		"github_mutation_audit",
		"figma_space_bindings",
		"figma_webhook_subscriptions",
		"figma_content_records",
		"figma_webhook_deliveries",
		"figma_comment_audit",
		"mcp_remote_connections",
		"mcp_discovery_snapshots",
		"mcp_remote_tools",
		"personal_agent_mcp_tools",
		"mcp_tool_execution_audit",
		"workflow_device_node_jobs",
		"space_events",
		"space_inbox_items",
		"space_tasks",
		"space_calendar_sources",
		"space_discord_links",
		"space_slack_links",
		"abuse_blocks",
		"space_calendar_events",
		"provider_shared_resources",
		"provider_content_records",
		"provider_gateway_state",
		"realtime_tickets",
		"space_resolve_tickets",
		"space_setup_integrations",
		"space_creation_requests",
	}

	for _, table := range tables {
		var rowSecurityEnabled bool
		var rowSecurityForced bool
		err := database.Conn.QueryRow(
			`SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE oid = $1::regclass`,
			table,
		).Scan(&rowSecurityEnabled, &rowSecurityForced)
		if err != nil {
			t.Fatalf("failed to inspect RLS settings for %s: %v", table, err)
		}
		if !rowSecurityEnabled {
			t.Fatalf("%s has row-level security disabled", table)
		}
		if !rowSecurityForced {
			t.Fatalf("%s does not force row-level security for table owners", table)
		}

		var policyCount int
		err = database.Conn.QueryRow(
			`SELECT COUNT(*) FROM pg_policies WHERE schemaname = 'public' AND tablename = $1`,
			table,
		).Scan(&policyCount)
		if err != nil {
			t.Fatalf("failed to inspect RLS policies for %s: %v", table, err)
		}
		if policyCount == 0 {
			t.Fatalf("%s has RLS enabled without policies", table)
		}
	}
}
