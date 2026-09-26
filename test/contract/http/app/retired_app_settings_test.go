package app

import (
	. "github.com/kannachi323/misty/server/internal/app"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRetiredAppSettingsRoutesAreAbsent(t *testing.T) {
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
			{"GET", "/me/apps/inbox/personal-connections"},
			{"PUT", "/me/apps/inbox/personal-connections"},
			{"GET", "/me/apps/inbox/imported-records"},
		} {
			response := httptest.NewRecorder()
			server.Router.ServeHTTP(response, httptest.NewRequest(route.method, prefix+route.path, nil))
			if response.Code != http.StatusNotFound {
				t.Fatalf("%s %s: expected 404, got %d", route.method, prefix+route.path, response.Code)
			}
		}
	}
}
