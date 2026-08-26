package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/kannachi323/misty/server/internal/app"
)

func TestAgentRoutesRequireAuthentication(t *testing.T) {
	configureJournalCollabForTest(t)
	t.Setenv("PASSWORD_RESET_URL", "http://localhost:5173/reset")
	t.Setenv("PASSWORD_RESET_START_URL", "http://localhost:8080/auth/reset/start")
	t.Setenv("MAILJET_API_KEY", "")
	t.Setenv("MAILJET_SECRET_KEY", "")
	t.Setenv("MAILJET_FROM_EMAIL", "")
	t.Setenv("MISTY_DEVICE_JOBS_ENABLED", "true")
	server, err := CreateServer()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.MountHandlers(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/devices",
		"/api/agents",
		"/api/spaces/space-1/tasks",
		"/api/spaces/space-1/calendar/events",
		"/api/ai/models",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.Router.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s status = %d, want 401", path, rec.Code)
		}
	}
}

func TestCustomAgentMutationAndInvocationRoutesAreAbsent(t *testing.T) {
	configureJournalCollabForTest(t)
	server, err := CreateServer()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.MountHandlers(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodPost, "/api/agents", http.StatusMethodNotAllowed},
		{http.MethodPatch, "/api/agents/personal-1", http.StatusMethodNotAllowed},
		{http.MethodDelete, "/api/agents/personal-1", http.StatusMethodNotAllowed},
		{http.MethodPut, "/api/agents/personal-1/avatar", http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/spaces/space-1/agents/personal-1/runs", http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/agents/delegate", http.StatusMethodNotAllowed},
		{http.MethodPut, "/api/agents/personal-1/mcp-tools", http.StatusNotFound},
		{http.MethodGet, "/api/agents/personal-1/mcp-executions", http.StatusNotFound},
		{http.MethodPost, "/api/agent-voice/speech", http.StatusNotFound},
		{http.MethodPost, "/api/spaces/space-1/conversations/direct", http.StatusMethodNotAllowed},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		server.Router.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
		if recorder.Code != test.want {
			t.Fatalf("%s %s status = %d, want %d", test.method, test.path, recorder.Code, test.want)
		}
	}
}

func TestRetiredAgentChatRoutesAreAbsent(t *testing.T) {
	configureJournalCollabForTest(t)
	t.Setenv("PASSWORD_RESET_URL", "http://localhost:5173/reset")
	t.Setenv("PASSWORD_RESET_START_URL", "http://localhost:8080/auth/reset/start")
	server, err := CreateServer()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.MountHandlers(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/agent-conversations",
		"/api/ai/sessions",
		"/api/mika/discovery",
	} {
		recorder := httptest.NewRecorder()
		server.Router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404", path, recorder.Code)
		}
	}
}
