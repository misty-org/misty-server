package db

import (
	"testing"
)

func TestArchivedBillingIsNotAccessibleToApplicationRole(t *testing.T) {
	database := openTestDatabase(t)
	var hidden, archived bool
	if err := database.Conn.QueryRow(`SELECT CASE WHEN to_regnamespace('misty_archive') IS NULL THEN true ELSE NOT has_schema_privilege('misty_app','misty_archive','USAGE') END,to_regclass('public.hosted_ai_wallets') IS NULL AND to_regclass('public.payment_purchase_reversals') IS NULL`).Scan(&hidden, &archived); err != nil || !hidden || !archived {
		t.Fatalf("billing archive exposed: hidden=%v archived=%v err=%v", hidden, archived, err)
	}
}

func TestTablesHaveRowLevelSecurityEnabled(t *testing.T) {
	database := openTestDatabase(t)

	tables := []string{
		"users",
		"licenses",
		"sessions",
		"password_reset_tokens",
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
		"space_integrations",
		"space_runs",
		"space_run_actions",
		"space_run_steps",
		"space_provider_credentials",
		"connected_accounts",
		"connected_account_oauth_states",
		"cloud_connections",
		"misty_ask_identities",
		"misty_ask_identity_versions",
		"misty_ask_conversations",
		"misty_ask_conversation_events",
		"workflow_device_node_jobs",
		"space_events",
		"space_tasks",
		"space_calendar_sources",
		"abuse_blocks",
		"space_calendar_events",
		"realtime_tickets",
		"space_resolve_tickets",
		"space_setup_integrations",
		"space_creation_requests",
		"workflow_schedules",
		"ai_conversation_modes",
		"agent_question_sets",
		"agent_plans",
		"agent_goals",
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
