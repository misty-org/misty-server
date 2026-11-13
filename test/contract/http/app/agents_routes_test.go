package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/misty-org/misty-server/internal/app"
)

func TestAgentRoutesRequireAuthentication(t *testing.T) {
	configureJournalCollabForTest(t)
	server, err := CreateServer()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.MountHandlers(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/v1/spaces/space-1/notes",
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
		{http.MethodGet, "/v1/agents", http.StatusNotFound},
		{http.MethodPost, "/v1/spaces/space-1/agents", http.StatusMethodNotAllowed},
		{http.MethodGet, "/v1/spaces/space-1/chat-agents", http.StatusNotFound},
		{http.MethodPut, "/v1/settings/companion-agent", http.StatusNotFound},
		{http.MethodPost, "/v1/agents", http.StatusNotFound},
		{http.MethodPatch, "/v1/agents/personal-1", http.StatusNotFound},
		{http.MethodDelete, "/v1/agents/personal-1", http.StatusNotFound},
		{http.MethodPut, "/v1/agents/personal-1/avatar", http.StatusNotFound},
		{http.MethodPost, "/v1/spaces/space-1/agents/personal-1/runs", http.StatusNotFound},
		{http.MethodPost, "/v1/agents/delegate", http.StatusNotFound},
		{http.MethodPut, "/v1/agents/personal-1/mcp-tools", http.StatusNotFound},
		{http.MethodGet, "/v1/agents/personal-1/mcp-executions", http.StatusNotFound},
		{http.MethodPost, "/v1/spaces/space-1/conversations/direct", http.StatusMethodNotAllowed},
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
	server, err := CreateServer()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.MountHandlers(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/v1/agent-conversations",
		"/v1/ai/sessions",
		"/v1/mika/discovery",
	} {
		recorder := httptest.NewRecorder()
		server.Router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404", path, recorder.Code)
		}
	}
}
