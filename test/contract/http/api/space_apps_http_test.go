package api

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	. "github.com/misty-org/misty-server/internal/platform/httpapi"
)

// A Space's apps over the routes the host calls: a mod installs, moves and removes an app, an app keeps
// small JSON per Space, removing it drops that storage, and outsiders reach none of it.
func TestSpaceAppRoutesInstallStoreAndRemove(t *testing.T) {
	database := openPresenceTestDatabase(t)
	ctx := context.Background()
	spaces, err := NewSpacesService(database, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Get("/spaces/{spaceID}/apps", spaces.SpaceApps())
	router.Put("/spaces/{spaceID}/apps/{appID}", spaces.SpaceApp())
	router.Delete("/spaces/{spaceID}/apps/{appID}", spaces.SpaceApp())
	router.Get("/spaces/{spaceID}/apps/{appID}/storage", spaces.SpaceAppStorage())
	router.Get("/spaces/{spaceID}/apps/{appID}/storage/{key}", spaces.SpaceAppStorageKey())
	router.Put("/spaces/{spaceID}/apps/{appID}/storage/{key}", spaces.SpaceAppStorageKey())
	router.Delete("/spaces/{spaceID}/apps/{appID}/storage/{key}", spaces.SpaceAppStorageKey())

	owner, err := database.CreateUser(uniqueTestEmail("apps-owner"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := database.CreateUser(uniqueTestEmail("apps-outsider"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Apps")
	if err != nil {
		t.Fatal(err)
	}
	token := newConversationTestBearerToken(t, database, owner.ID)
	outsiderToken := newConversationTestBearerToken(t, database, outsider.ID)
	base := "/spaces/" + space.ID + "/apps"
	call := func(method, target string, body any) *httptest.ResponseRecorder {
		return performConversationRequest(t, router, method, target, token, body)
	}

	plannerJSON(t, call(http.MethodPut, base+"/demo", map[string]any{"version": "1.0.0", "permissions": []string{"records", "members", "records"}}), http.StatusOK)
	plannerJSON(t, call(http.MethodPut, base+"/Bad_Id", map[string]any{"version": "1", "permissions": []string{}}), http.StatusBadRequest)
	plannerJSON(t, call(http.MethodPut, base+"/demo", map[string]any{"version": "1", "permissions": []string{"root"}}), http.StatusBadRequest)

	appNamed := func(id string) map[string]any {
		for _, item := range plannerJSON(t, call(http.MethodGet, base, nil), http.StatusOK)["apps"].([]any) {
			if app := item.(map[string]any); app["app_id"] == id {
				return app
			}
		}
		return nil
	}
	// Misty's own apps are built in: they are never recorded, and can't be installed.
	plannerJSON(t, call(http.MethodPut, base+"/docs", map[string]any{"version": "1", "permissions": []string{}}), http.StatusBadRequest)
	if appNamed("docs") != nil {
		t.Fatal("a built-in app should not be recorded as installed")
	}
	app := appNamed("demo")
	if app == nil || app["app_id"] != "demo" || app["version"] != "1.0.0" || len(app["permissions"].([]any)) != 2 {
		t.Fatalf("installed app = %v", app)
	}

	// An app moves to a new version in place.
	plannerJSON(t, call(http.MethodPut, base+"/demo", map[string]any{"version": "1.1.0", "permissions": []string{"records"}}), http.StatusOK)
	if got := appNamed("demo"); got == nil || got["version"] != "1.1.0" {
		t.Fatalf("app after update = %v", got)
	}

	key := base + "/demo/storage/view%20mode"
	plannerJSON(t, call(http.MethodPut, key, map[string]any{"value": map[string]any{"layout": "board"}}), http.StatusOK)
	got := plannerJSON(t, call(http.MethodGet, key, nil), http.StatusOK)
	if got["key"] != "view mode" || got["value"].(map[string]any)["layout"] != "board" {
		t.Fatalf("stored = %v", got)
	}
	plannerJSON(t, call(http.MethodPut, base+"/demo/storage/other", map[string]any{"value": 3}), http.StatusOK)
	values := plannerJSON(t, call(http.MethodGet, base+"/demo/storage?prefix=view", nil), http.StatusOK)["values"].([]any)
	if len(values) != 1 {
		t.Fatalf("prefix list = %v", values)
	}
	// Storage of an app that isn't installed isn't there.
	plannerJSON(t, call(http.MethodGet, base+"/other/storage/x", nil), http.StatusNotFound)
	plannerJSON(t, call(http.MethodPut, base+"/other/storage/x", map[string]any{"value": 1}), http.StatusNotFound)

	// People outside the Space reach nothing, and cannot install.
	for _, request := range []struct{ method, path string }{
		{http.MethodGet, base}, {http.MethodPut, base + "/other"}, {http.MethodDelete, base + "/demo"},
		{http.MethodGet, key}, {http.MethodPut, key},
	} {
		body := any(nil)
		if request.method == http.MethodPut {
			body = map[string]any{"version": "1", "permissions": []string{}}
			if strings.Contains(request.path, "/storage/") {
				body = map[string]any{"value": 1}
			}
		}
		response := performConversationRequest(t, router, request.method, request.path, outsiderToken, body)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s %s as an outsider = %d, want 403", request.method, request.path, response.Code)
		}
	}

	// Removing the app drops its storage; reinstalling starts empty.
	plannerJSON(t, call(http.MethodDelete, base+"/demo", nil), http.StatusNoContent)
	plannerJSON(t, call(http.MethodDelete, base+"/demo", nil), http.StatusNotFound)
	plannerJSON(t, call(http.MethodPut, base+"/demo", map[string]any{"version": "1.1.0", "permissions": []string{}}), http.StatusOK)
	plannerJSON(t, call(http.MethodGet, key, nil), http.StatusNotFound)
}
