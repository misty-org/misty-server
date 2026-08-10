package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/app"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestHealthIsSanitizedAndFailsWhenCriticalDependenciesAreUnavailable(t *testing.T) {
	secret := "must-not-appear-in-health-response"
	for _, key := range []string{
		"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET",
		"SLACK_CLIENT_ID", "SLACK_CLIENT_SECRET", "SLACK_SIGNING_SECRET",
		"NOTION_CLIENT_ID", "NOTION_CLIENT_SECRET", "NOTION_WEBHOOK_VERIFICATION_TOKEN",
		"DISCORD_CLIENT_ID", "DISCORD_CLIENT_SECRET", "DISCORD_BOT_TOKEN",
	} {
		t.Setenv(key, secret)
	}
	server := &Server{Database: &db.Database{}, LibraryStore: api.NewMemoryLibraryObjectStore()}
	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	recorder := httptest.NewRecorder()

	TestingNewHealthMonitor(server).Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), secret) {
		t.Fatal("health response exposed a configured secret")
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var snapshot TestingHealthSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if snapshot.Status != "unavailable" || snapshot.Checks["database"].Status != "unavailable" || snapshot.Checks["realtime"].Status != "unavailable" {
		t.Fatalf("unexpected critical health: %+v", snapshot)
	}
	for _, provider := range []string{"google", "slack", "notion"} {
		if snapshot.Checks[provider].Status != "ready" {
			t.Fatalf("%s health = %+v, want configured readiness", provider, snapshot.Checks[provider])
		}
	}
}

func TestSummarizeHealthUses503OnlyForCriticalFailures(t *testing.T) {
	status, code := TestingSummarizeHealth(map[string]TestingHealthCheck{
		"database": {Status: "ok", Critical: true},
		"notion":   {Status: "unconfigured", Critical: false},
	})
	if status != "degraded" || code != http.StatusOK {
		t.Fatalf("optional failure summary = (%q,%d), want (degraded,200)", status, code)
	}
	status, code = TestingSummarizeHealth(map[string]TestingHealthCheck{
		"database": {Status: "unavailable", Critical: true},
	})
	if status != "unavailable" || code != http.StatusServiceUnavailable {
		t.Fatalf("critical failure summary = (%q,%d), want (unavailable,503)", status, code)
	}
}

func TestPublicAPIHealthRequiresHTTPSInProduction(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "production")
	t.Setenv("MISTY_PUBLIC_API_URL", "http://mistysys.com/api")
	if got := TestingPublicAPIConfigurationCheck(); got.Status != "degraded" {
		t.Fatalf("HTTP production API health = %+v, want degraded", got)
	}
	t.Setenv("MISTY_PUBLIC_API_URL", "https://mistysys.com/api")
	if got := TestingPublicAPIConfigurationCheck(); got.Status != "ready" {
		t.Fatalf("HTTPS production API health = %+v, want ready", got)
	}
}
