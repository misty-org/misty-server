package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/app"
)

func TestMCPServerIsCanonicalPOSTAndRequiresBearerAuthentication(t *testing.T) {
	configureJournalCollabForTest(t)
	server, err := CreateServer()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.MountHandlers(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{}}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.Router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Fatalf("POST /mcp status=%d auth=%q body=%s", recorder.Code, recorder.Header().Get("WWW-Authenticate"), recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	server.Router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp status=%d, want 405", recorder.Code)
	}
}
