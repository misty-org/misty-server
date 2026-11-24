package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/misty-org/misty-server/internal/app"
)

func TestMetricsEndpointThroughTheRealRouter(t *testing.T) {
	t.Setenv("MISTY_METRICS_TOKEN", "e2e-token-value")

	server, err := CreateServer()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.MountHandlers(); err != nil {
		t.Fatal(err)
	}

	for range 3 {
		server.Router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/spaces/space-1/notes", nil))
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
		`route="/v1/spaces/{spaceID}/notes"`,
		"misty_realtime_connections",
		"misty_library_jobs_pending_ai",
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
