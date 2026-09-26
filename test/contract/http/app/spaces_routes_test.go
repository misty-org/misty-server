package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Restoring the browser's Spaces UI must not depend on retired app routes.
func TestSpacesToolsRemainMountedAndAuthenticated(t *testing.T) {
	server := noteRouteTestServer(t)
	for _, prefix := range []string{"", "/api", "/v1"} {
		for _, endpoint := range []struct{ method, path string }{
			{"GET", "/spaces"}, {"POST", "/spaces"},
			{"GET", "/spaces/example"}, {"GET", "/spaces/example/members"},
			{"GET", "/spaces/example/messages"}, {"POST", "/spaces/example/messages"},
			{"GET", "/spaces/example/conversations"},
			{"GET", "/spaces/example/tasks"}, {"POST", "/spaces/example/tasks"},
			{"GET", "/spaces/example/agenda"}, {"GET", "/spaces/example/notes"},
			{"GET", "/spaces/example/drawings"}, {"GET", "/spaces/example/library"},
			{"POST", "/spaces/invitations/example/accept"},
		} {
			t.Run(endpoint.method+prefix+endpoint.path, func(t *testing.T) {
				response := httptest.NewRecorder()
				server.Router.ServeHTTP(response, httptest.NewRequest(endpoint.method, prefix+endpoint.path, nil))
				if response.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401", response.Code)
				}
			})
		}
	}
}
