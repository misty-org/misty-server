package app

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	. "github.com/misty-org/misty-server/internal/app"
)

func TestBrowserServerDoesNotMountRetiredProducts(t *testing.T) {
	configureJournalCollabForTest(t)
	t.Setenv("MISTY_ENVIRONMENT", "")
	t.Setenv("MISTY_METRICS_TOKEN", "")
	server, err := CreateServer()
	if err != nil {
		t.Fatal(err)
	}
	if err = server.MountHandlers(); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	err = chi.Walk(server.Router, func(method, path string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		normalized := strings.TrimPrefix(path, "/v1/")
		normalized = "/" + strings.TrimLeft(normalized, "/")
		for _, retired := range []string{"/stripe/webhook", "/billing", "/me/referral", "/internal/voice/live/settle", "/apps", "/me/apps", "/app-runtime", "/me/routines", "/me/routine-runs", "/automations", "/waitlist", "/activepieces", "/capabilities", "/me/sdk-targets", "/me/sdk-runs", "/me/capability-approvals", "/me/agent-approvals", "/me/agent-invocations", "/mcp/connections", "/studio", "/self-host", "/internal/self-host", "/billing/self-host-entitlement", "/ai/inbound-channels", "/sync", "/devices", "/auth/handoff", "/instance", "/me/device", "/me/telemetry"} {
			if normalized == retired || strings.HasPrefix(normalized, retired+"/") {
				t.Errorf("retired route remains: %s %s", method, path)
			}
		}
		if strings.Contains(path, "/routine-agent") || strings.Contains(path, "/routine-wait") || strings.Contains(path, "/automation-rules") {
			t.Errorf("retired automation route remains: %s", path)
		}
		// Clients use /v1 only. The bare /health is for Docker and compose
		// health checks; /metrics and the operator API have their own paths.
		if !strings.HasPrefix(path, "/v1/") && !strings.HasPrefix(path, "/operator/v1/") &&
			path != "/health" && path != "/metrics" {
			t.Errorf("route outside /v1: %s %s", method, path)
		}
		found[normalized] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"/spaces", "/spaces/{spaceID}/agents"} {
		if !found[required] {
			t.Errorf("retained route missing: %s", required)
		}
	}
}
