package app

import (
	. "github.com/kannachi323/misty/server/internal/app"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSharedSourceRoutesAreAbsent(t *testing.T) {
	configureJournalCollabForTest(t)
	server, err := CreateServer()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.MountHandlers(); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"", "/api", "/v1"} {
		for _, route := range []struct{ method, path string }{
			{"GET", "/provider-sources/availability"},
			{"GET", "/spaces/space-1/sources"},
			{"POST", "/spaces/space-1/sources"},
			{"POST", "/spaces/space-1/sources/preview"},
			{"POST", "/spaces/space-1/sources/source-1/control"},
			{"POST", "/spaces/space-1/sources/source-1/copy"},
			{"POST", "/webhooks/provider-sources/google-drive"},
			{"GET", "/spaces/space-1/provider-resources"},
			{"POST", "/spaces/space-1/provider-resources"},
			{"DELETE", "/spaces/space-1/provider-resources/resource-1"},
			{"GET", "/spaces/space-1/integrations/integration-1/resources"},
			{"PUT", "/spaces/space-1/integrations/integration-1/resources"},
		} {
			response := httptest.NewRecorder()
			server.Router.ServeHTTP(response, httptest.NewRequest(route.method, prefix+route.path, nil))
			if response.Code != http.StatusNotFound {
				t.Fatalf("%s %s: expected 404, got %d", route.method, prefix+route.path, response.Code)
			}
		}
	}
}
