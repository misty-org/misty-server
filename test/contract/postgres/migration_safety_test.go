package db

import (
	"os"
	"strings"
	"testing"
)

func TestUnifiedAgentMigrationPreservesSharedProductData(t *testing.T) {
	raw, err := os.ReadFile("../../../internal/platform/postgres/migrations/20260831000000_unified_agent_workflows_v2.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(raw))
	for _, destructive := range []string{
		"delete from space_agents",
		"delete from spaces",
		"delete from space_chats",
		"delete from library",
		"delete from provider_integrations",
		"delete from trusted_devices",
	} {
		if strings.Contains(sql, destructive) {
			t.Fatalf("migration contains prohibited destructive statement %q", destructive)
		}
	}
	for _, required := range []string{
		"insert into space_agent_versions",
		"drop table if exists agent_artifacts",
		"agent_definitions",
		"agent_jobs",
		"where resource_kind='workflow'",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration is missing scoped cutover guard %q", required)
		}
	}
}
