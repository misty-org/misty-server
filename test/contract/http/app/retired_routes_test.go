package app

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	. "github.com/kannachi323/misty/server/internal/app"
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
		normalized := strings.TrimPrefix(strings.TrimPrefix(path, "/api/"), "/v1/")
		normalized = "/" + strings.TrimLeft(normalized, "/")
		for _, retired := range []string{"/stripe/webhook", "/billing/trial/start", "/billing/credit-checkout-session", "/apps", "/me/apps", "/app-runtime", "/me/routines", "/me/routine-runs", "/automations", "/waitlist", "/activepieces", "/capabilities", "/me/sdk-targets", "/me/sdk-runs", "/me/capability-approvals", "/self-host/entitlement", "/billing/self-host-entitlement"} {
			if normalized == retired || strings.HasPrefix(normalized, retired+"/") {
				t.Errorf("retired route remains: %s %s", method, path)
			}
		}
		if strings.Contains(path, "/routine-agent") || strings.Contains(path, "/routine-wait") || strings.Contains(path, "/automation-rules") {
			t.Errorf("retired automation route remains: %s", path)
		}
		found[normalized] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"/sync/ws", "/sync/workspace", "/sync/devices", "/sync/control", "/spaces", "/mcp/connections", "/me/agent-approvals", "/me/agent-invocations/{runID}/approvals/{approvalID}", "/internal/agent-runtime/runs/{runID}/tools"} {
		if !found[required] {
			t.Errorf("retained route missing: %s", required)
		}
	}
}
