package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPublicBetaObservabilityArtifactsReferenceMistyMetrics(t *testing.T) {
	dashboard, err := os.ReadFile("../../../../deploy/observability/grafana-dashboard.json")
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(dashboard, &parsed); err != nil {
		t.Fatalf("dashboard JSON: %v", err)
	}
	if parsed["uid"] != "misty-public-beta" {
		t.Fatalf("dashboard uid = %#v", parsed["uid"])
	}

	rules, err := os.ReadFile("../../../../deploy/observability/prometheus-rules.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(rules)
	for _, required := range []string{
		"MistyAPIHighErrorRate",
		"MistyDatabasePoolNearExhaustion",
		"MistyJournalControlBacklog",
		"misty_upload_reservations_active",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("alert rules missing %q", required)
		}
	}
}
