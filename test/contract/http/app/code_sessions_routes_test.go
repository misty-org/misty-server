package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCodeSessionRoutesRequireAuthentication(t *testing.T) {
	server := noteRouteTestServer(t)
	for _, testCase := range []struct{ method, path string }{
		{http.MethodGet, "/v1/spaces/space-1/code-sessions"},
		{http.MethodPost, "/v1/spaces/space-1/code-sessions"},
		{http.MethodGet, "/v1/spaces/space-1/code-sessions/session-1"},
		{http.MethodPost, "/v1/spaces/space-1/code-sessions/session-1/end"},
		{http.MethodPost, "/v1/spaces/space-1/code-sessions/session-1/collaboration-ticket"},
	} {
		recorder := httptest.NewRecorder()
		server.Router.ServeHTTP(recorder, httptest.NewRequest(testCase.method, testCase.path, nil))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want 401", testCase.method, testCase.path, recorder.Code)
		}
	}
}

func TestCodeSessionRoutesAreMountedOnlyUnderV1(t *testing.T) {
	server := noteRouteTestServer(t)
	for _, path := range []string{"/spaces/space-1/code-sessions", "/api/spaces/space-1/code-sessions"} {
		recorder := httptest.NewRecorder()
		server.Router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", path, recorder.Code)
		}
	}
}
