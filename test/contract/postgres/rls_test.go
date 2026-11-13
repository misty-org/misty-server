package db

import (
	"testing"
)

func TestArchivedRecordsAreNotAccessibleToApplicationRole(t *testing.T) {
	database := openTestDatabase(t)
	var hidden, archived bool
	if err := database.Conn.QueryRow(`SELECT CASE WHEN to_regnamespace('misty_archive') IS NULL THEN true ELSE NOT has_schema_privilege('misty_app','misty_archive','USAGE') END,to_regclass('public.hosted_ai_wallets') IS NULL AND to_regclass('public.payment_purchase_reversals') IS NULL`).Scan(&hidden, &archived); err != nil || !hidden || !archived {
		t.Fatalf("archive exposed: hidden=%v archived=%v err=%v", hidden, archived, err)
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
		"spaces",
		"space_members",
		"space_invitations",
		"space_messages",
		"space_message_reactions",
		"space_conversations",
		"space_conversation_members",
		"space_nodes",
		"space_integrations",
		"space_agents",
		"space_agent_artifacts",
		"space_provider_credentials",
		"cloud_connections",
		"space_events",
		"abuse_blocks",
		"realtime_tickets",
		"space_resolve_tickets",
		"space_creation_requests",
		"account_controls",
		"account_setup_keys",
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
