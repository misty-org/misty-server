package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/app"
)

// The endpoint must be mounted only when a token is configured, and must be
// reachable through the real router with all its middleware in place.
func TestMetricsEndpointThroughTheRealRouter(t *testing.T) {
	configureJournalCollabForTest(t)
	t.Setenv("PASSWORD_RESET_URL", "http://localhost:5173/reset")
	t.Setenv("PASSWORD_RESET_START_URL", "http://localhost:8080/auth/reset/start")
	t.Setenv("MAILJET_API_KEY", "")
	t.Setenv("MAILJET_SECRET_KEY", "")
	t.Setenv("MAILJET_FROM_EMAIL", "")
	t.Setenv("MISTY_METRICS_TOKEN", "e2e-token-value")

	server, err := CreateServer()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.MountHandlers(); err != nil {
		t.Fatal(err)
	}

	// Generate some traffic so tier 2 has something to report.
	for range 3 {
		server.Router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/spaces/space-1/notes", nil))
	}

	authorized := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	authorized.Header.Set("Authorization", "Bearer e2e-token-value")
	recorder := httptest.NewRecorder()
	server.Router.ServeHTTP(recorder, authorized)
	if recorder.Code != http.StatusOK {
		t.Fatalf("authorized scrape = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	for _, want := range []string{
		"go_goroutines",
		"misty_http_requests_total",
		`route="/api/spaces/{spaceID}/notes"`,
		"misty_realtime_connections",
		"misty_note_control_backlog",
		"misty_library_jobs_pending_rendition",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("scrape missing %q", want)
		}
	}
	if strings.Contains(body, "space-1") {
		t.Fatal("a concrete space id leaked into a metric label")
	}

	unauthorized := httptest.NewRecorder()
	server.Router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if unauthorized.Code != http.StatusNotFound {
		t.Fatalf("unauthorized scrape = %d, want 404", unauthorized.Code)
	}
}
