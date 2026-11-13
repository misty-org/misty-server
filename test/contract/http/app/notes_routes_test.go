package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/misty-org/misty-server/internal/app"
)

func noteRouteTestServer(t *testing.T) *Server {
	t.Helper()
	configureJournalCollabForTest(t)
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
		{http.MethodGet, "/v1/spaces/space-1/notes"},
		{http.MethodPost, "/v1/spaces/space-1/notes"},
		{http.MethodGet, "/v1/spaces/space-1/notes/note-1"},
		{http.MethodDelete, "/v1/spaces/space-1/notes/note-1"},
		{http.MethodPatch, "/v1/spaces/space-1/notes/note-1/metadata"},
		{http.MethodPatch, "/v1/spaces/space-1/notes/note-1"},
		{http.MethodPost, "/v1/spaces/space-1/notes/note-1/collaboration-ticket"},
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

// The routes exist only on the /v1 tree.
func TestNoteRoutesAreMountedOnlyUnderV1(t *testing.T) {
	server := noteRouteTestServer(t)

	for _, path := range []string{"/spaces/space-1/notes", "/api/spaces/space-1/notes"} {
		recorder := httptest.NewRecorder()
		server.Router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", path, recorder.Code)
		}
	}
	for _, path := range []string{
		"/v1/spaces/space-1/notes",
	} {
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
