package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	. "github.com/misty-org/misty-server/internal/platform/httpapi"
)

func routeJSON(t *testing.T, response *httptest.ResponseRecorder, want int) map[string]any {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d: %s", response.Code, want, response.Body.String())
	}
	out := map[string]any{}
	if response.Body.Len() == 0 {
		return out
	}
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSpaceAppRoutesInstallAndRemove(t *testing.T) {
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

	routeJSON(t, call(http.MethodPut, base+"/demo", map[string]any{"version": "1.0.0", "permissions": []string{"files", "members", "files"}}), http.StatusOK)
	routeJSON(t, call(http.MethodPut, base+"/Bad_Id", map[string]any{"version": "1", "permissions": []string{}}), http.StatusBadRequest)
	routeJSON(t, call(http.MethodPut, base+"/demo", map[string]any{"version": "1", "permissions": []string{"root"}}), http.StatusBadRequest)
	routeJSON(t, call(http.MethodPut, base+"/demo", map[string]any{"version": "1", "permissions": []string{"records"}}), http.StatusBadRequest)

	appNamed := func(id string) map[string]any {
		for _, item := range routeJSON(t, call(http.MethodGet, base, nil), http.StatusOK)["apps"].([]any) {
			if app := item.(map[string]any); app["app_id"] == id {
				return app
			}
		}
		return nil
	}
	app := appNamed("demo")
	if app == nil || app["app_id"] != "demo" || app["version"] != "1.0.0" || len(app["permissions"].([]any)) != 2 {
		t.Fatalf("installed app = %v", app)
	}
	if _, has := app["source"]; has {
		t.Fatalf("a catalog app carries a source: %v", app)
	}

	routeJSON(t, call(http.MethodPut, base+"/docs", map[string]any{"version": "1", "permissions": []string{}}), http.StatusOK)
	if appNamed("docs") == nil {
		t.Fatal("docs was not recorded as installed")
	}

	routeJSON(t, call(http.MethodPut, base+"/demo", map[string]any{"version": "1.1.0", "permissions": []string{"rooms"}}), http.StatusOK)
	if got := appNamed("demo"); got == nil || got["version"] != "1.1.0" {
		t.Fatalf("app after update = %v", got)
	}

	source := map[string]any{
		"repo": "maker/pinned", "listed": false, "manifest_from": "basic",
		"entry": map[string]any{"id": "pinned", "name": "Pinned", "version": "0.3.0", "sha256": "abc123", "permissions": []any{"files"}},
	}
	installed := routeJSON(t, call(http.MethodPut, base+"/pinned", map[string]any{"version": "0.3.0", "permissions": []string{"files"}, "source": source}), http.StatusOK)
	if got, _ := installed["source"].(map[string]any); got == nil || got["repo"] != "maker/pinned" || got["listed"] != false {
		t.Fatalf("install response source = %v", installed["source"])
	}
	listed := appNamed("pinned")
	got, _ := listed["source"].(map[string]any)
	entry, _ := got["entry"].(map[string]any)
	if got == nil || got["manifest_from"] != "basic" || entry == nil || entry["sha256"] != "abc123" {
		t.Fatalf("listed source = %v", listed["source"])
	}
	routeJSON(t, call(http.MethodPut, base+"/pinned", map[string]any{"version": "1.0.0", "permissions": []string{}}), http.StatusOK)
	if _, has := appNamed("pinned")["source"]; has {
		t.Fatal("source kept after a reinstall without one")
	}
	routeJSON(t, call(http.MethodPut, base+"/pinned", map[string]any{"version": "1", "permissions": []string{}, "source": []any{1}}), http.StatusBadRequest)
	routeJSON(t, call(http.MethodPut, base+"/pinned", map[string]any{"version": "1", "permissions": []string{}, "source": "maker/pinned"}), http.StatusBadRequest)
	routeJSON(t, call(http.MethodPut, base+"/pinned", map[string]any{"version": "1", "permissions": []string{},
		"source": map[string]any{"repo": strings.Repeat("x", 64<<10)}}), http.StatusBadRequest)

	for _, request := range []struct{ method, path string }{
		{http.MethodGet, base}, {http.MethodPut, base + "/other"}, {http.MethodDelete, base + "/demo"},
	} {
		body := any(nil)
		if request.method == http.MethodPut {
			body = map[string]any{"version": "1", "permissions": []string{}}
		}
		response := performConversationRequest(t, router, request.method, request.path, outsiderToken, body)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s %s as an outsider = %d, want 403", request.method, request.path, response.Code)
		}
	}

	routeJSON(t, call(http.MethodDelete, base+"/demo", nil), http.StatusNoContent)
	routeJSON(t, call(http.MethodDelete, base+"/demo", nil), http.StatusNotFound)
	if appNamed("demo") != nil {
		t.Fatal("removed app still listed")
	}
}
