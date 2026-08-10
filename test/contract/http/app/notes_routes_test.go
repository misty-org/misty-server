package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/kannachi323/misty/server/internal/app"
)

func noteRouteTestServer(t *testing.T) *Server {
	t.Helper()
	configureJournalCollabForTest(t)
	t.Setenv("PASSWORD_RESET_URL", "http://localhost:5173/reset")
	t.Setenv("PASSWORD_RESET_START_URL", "http://localhost:8080/auth/reset/start")
	t.Setenv("MAILJET_API_KEY", "")
	t.Setenv("MAILJET_SECRET_KEY", "")
	t.Setenv("MAILJET_FROM_EMAIL", "")
	server, err := CreateServer()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.MountHandlers(); err != nil {
		t.Fatal(err)
	}
	return server
}

// Every note route must reject an unauthenticated caller before it does any
// note lookup, so an anonymous request can never probe for note existence.
func TestNoteRoutesRequireAuthentication(t *testing.T) {
	server := noteRouteTestServer(t)

	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/spaces/space-1/notes"},
		{http.MethodPost, "/api/spaces/space-1/notes"},
		{http.MethodGet, "/api/spaces/space-1/notes/note-1"},
		{http.MethodDelete, "/api/spaces/space-1/notes/note-1"},
		{http.MethodPatch, "/api/spaces/space-1/notes/note-1/metadata"},
		{http.MethodPatch, "/api/spaces/space-1/notes/note-1"},
		{http.MethodPost, "/api/spaces/space-1/notes/note-1/collaboration-ticket"},
	}

	for _, testCase := range cases {
		request := httptest.NewRequest(testCase.method, testCase.path, nil)
		recorder := httptest.NewRecorder()
		server.Router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want 401", testCase.method, testCase.path, recorder.Code)
		}
	}
}

// The routes must exist on both the bare and /api-prefixed trees, matching how
// every other Space route is mounted for older clients.
func TestNoteRoutesAreMountedOnBothPrefixes(t *testing.T) {
	server := noteRouteTestServer(t)

	for _, path := range []string{"/spaces/space-1/notes", "/api/spaces/space-1/notes"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		server.Router.ServeHTTP(recorder, request)
		// 401 proves the route resolved to the handler; 404 would mean it is
		// not registered at all.
		if recorder.Code == http.StatusNotFound {
			t.Fatalf("GET %s returned 404, want the route to be mounted", path)
		}
	}
}
