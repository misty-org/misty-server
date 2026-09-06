package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/kannachi323/misty/server/internal/appcatalog"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestEveryOfficialAppCatalogGrantMatchesItsRuntimeOperations(t *testing.T) {
	type operation struct {
		method, path, scope string
	}
	operations := map[string][]operation{
		"chat": {
			{http.MethodGet, "/v1/spaces/space_1", "spaces.read"},
			{http.MethodGet, "/v1/spaces/space_1/messages", "messages.read"},
			{http.MethodPost, "/v1/spaces/space_1/messages", "messages.write"},
			{http.MethodGet, "/v1/connections", "connections.read"},
			{http.MethodPost, "/v1/connections/google/authorize", "connections.write"},
			{http.MethodGet, "/v1/ai/models", "ai.read"},
			{http.MethodPost, "/v1/ai/complete", "ai.write"},
		},
		"journal": {
			{http.MethodGet, "/v1/spaces/space_1/notes", "notes.read"},
			{http.MethodPost, "/v1/spaces/space_1/notes", "notes.write"},
			{http.MethodGet, "/v1/spaces/space_1/drawings", "drawings.read"},
			{http.MethodPatch, "/v1/spaces/space_1/drawings/drawing_1", "drawings.write"},
			{http.MethodGet, "/v1/me", "profile.read"},
		},
		"planner": {
			{http.MethodGet, "/v1/spaces/space_1/tasks", "tasks.read"},
			{http.MethodPatch, "/v1/spaces/space_1/tasks/task_1", "tasks.write"},
			{http.MethodGet, "/v1/spaces/space_1/calendar/events", "calendar.read"},
			{http.MethodPost, "/v1/spaces/space_1/calendar/events", "calendar.write"},
			{http.MethodGet, "/v1/spaces/space_1/roadmaps", "roadmaps.read"},
			{http.MethodPost, "/v1/spaces/space_1/roadmaps", "roadmaps.write"},
		},
		"library": {
			{http.MethodGet, "/v1/spaces/space_1/library", "library.read"},
			{http.MethodPost, "/v1/spaces/space_1/library", "library.write"},
		},
		"inbox": {
			{http.MethodGet, "/v1/activity/inbox", "activity.read"},
			{http.MethodPost, "/v1/activity/inbox/seen", "activity.write"},
			{http.MethodGet, "/v1/mail/threads", "mail.read"},
			{http.MethodPost, "/v1/mail/drafts", "mail.write"},
		},
		"agents": {
			{http.MethodGet, "/v1/agents", "agents.read"},
			{http.MethodPost, "/v1/agents", "agents.write"},
			{http.MethodGet, "/v1/mcp/servers", "mcp.read"},
			{http.MethodPost, "/v1/mcp/servers", "mcp.write"},
			{http.MethodGet, "/v1/automations", "automations.read"},
			{http.MethodPost, "/v1/automations", "automations.write"},
			{http.MethodGet, "/v1/devices", "devices.read"},
			{http.MethodPost, "/v1/devices", "devices.write"},
		},
		"files": {
			{http.MethodGet, "/v1/spaces/space_1/attachments", "files.read"},
			{http.MethodPost, "/v1/spaces/space_1/attachments", "files.write"},
			{http.MethodGet, "/v1/ai/media-search/results", "media-search.read"},
			{http.MethodPost, "/v1/ai/media-search/index", "media-search.write"},
			{http.MethodGet, "/v1/search/global", "search.read"},
		},
	}

	for _, appID := range []string{"chat", "journal", "planner", "library", "inbox", "agents", "files"} {
		app, ok := appcatalog.Find(appID)
		if !ok {
			t.Fatalf("catalog is missing %s", appID)
		}
		for _, item := range operations[appID] {
			session := db.AppRuntimeSession{AppID: appID, SpaceID: "space_1", Scopes: app.Scopes}
			if !TestingAuthorizeAppRuntimeRequest(session, item.method, item.path) {
				t.Errorf("%s catalog grants do not allow %s %s", appID, item.method, item.path)
			}
			session.Scopes = withoutScope(app.Scopes, item.scope)
			if TestingAuthorizeAppRuntimeRequest(session, item.method, item.path) {
				t.Errorf("%s %s %s did not require %s", appID, item.method, item.path, item.scope)
			}
			if len(strings.Split(strings.Trim(item.path, "/"), "/")) >= 3 && strings.Contains(item.path, "/spaces/space_1/") {
				session.Scopes = app.Scopes
				session.SpaceID = "space_2"
				if TestingAuthorizeAppRuntimeRequest(session, item.method, item.path) {
					t.Errorf("%s %s escaped its active Space", appID, item.path)
				}
			}
		}
	}

	for _, appID := range []string{"browser", "code", "terminal"} {
		app, ok := appcatalog.Find(appID)
		if !ok {
			t.Fatalf("catalog is missing %s", appID)
		}
		session := db.AppRuntimeSession{AppID: appID, SpaceID: "space_1", Scopes: app.Scopes}
		if TestingAuthorizeAppRuntimeRequest(session, http.MethodGet, "/v1/tasks") {
			t.Errorf("local-only %s gained an undeclared HTTP domain", appID)
		}
	}
}

func withoutScope(scopes []string, removed string) []string {
	result := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if scope != removed {
			result = append(result, scope)
		}
	}
	return result
}

func TestAppRuntimeAuthorizationIsSpaceBoundAndDenyByDefault(t *testing.T) {
	session := db.AppRuntimeSession{
		AppID: "planner", SpaceID: "space_1",
		Scopes: []string{"spaces.read", "tasks.read", "tasks.write", "roadmaps.read"},
	}
	tests := []struct {
		method, path string
		allowed      bool
	}{
		{http.MethodGet, "/v1/spaces", false},
		{http.MethodGet, "/api/spaces/space_1/tasks", true},
		{http.MethodPost, "/v1/spaces/space_1/tasks", true},
		{http.MethodGet, "/v1/spaces/space_1/roadmaps", true},
		{http.MethodPost, "/v1/spaces/space_1/roadmaps", false},
		{http.MethodGet, "/v1/spaces/space_2/tasks", false},
		{http.MethodGet, "/v1/me", false},
		{http.MethodDelete, "/v1/me/apps/planner", false},
		{http.MethodGet, "/v1/app-runtime/records", false},
		{http.MethodPost, "/v1/app-runtime/session", false},
		{http.MethodDelete, "/v1/app-runtime/records", false},
	}
	for _, test := range tests {
		if got := TestingAuthorizeAppRuntimeRequest(session, test.method, test.path); got != test.allowed {
			t.Errorf("authorize(%s %s) = %v, want %v", test.method, test.path, got, test.allowed)
		}
	}
	withoutSpace := session
	withoutSpace.SpaceID = ""
	if TestingAuthorizeAppRuntimeRequest(withoutSpace, http.MethodGet, "/v1/spaces/space_1/tasks") {
		t.Fatal("an unbound app session accessed a Space resource")
	}
}

func TestAppCalendarCapabilitiesAreExplicitAndSpaceBound(t *testing.T) {
	tests := []struct {
		method, path string
		scopes       []string
		allowed      bool
	}{
		{http.MethodGet, "calendar/sources", []string{"tasks.read"}, false},
		{http.MethodGet, "calendar/events", []string{"calendar.read"}, true},
		{http.MethodGet, "calendar/sources", []string{"calendar.read"}, true},
		{http.MethodPost, "calendar/events", []string{"calendar.read"}, false},
		{http.MethodPost, "calendar/events", []string{"calendar.write"}, true},
		{http.MethodPatch, "calendar/events/event_1", []string{"calendar.write"}, true},
		{http.MethodDelete, "calendar/sources/source_1", []string{"calendar.write"}, true},
		{http.MethodPost, "calendar/sync", []string{"calendar.write"}, false},
		{http.MethodPost, "calendar/sync", []string{"calendar.write", "tasks.write"}, true},
		{http.MethodGet, "calendar/google/calendars", []string{"calendar.read"}, false},
		{http.MethodGet, "calendar/google/calendars", []string{"calendar.read", "connections.read"}, true},
		{http.MethodGet, "integrations", []string{"connections.read"}, true},
		{http.MethodGet, "integrations/google/resources", []string{"connections.read"}, true},
		{http.MethodPost, "integrations/google/bind", []string{"connections.read"}, false},
		{http.MethodPost, "integrations/google/bind", []string{"connections.write"}, true},
		{http.MethodPost, "integrations/google/authorize", []string{"connections.write"}, true},
		{http.MethodPut, "integrations/google", []string{"connections.write"}, false},
		{http.MethodGet, "integrations/google/credentials", []string{"connections.read"}, false},
		{http.MethodPost, "calendar/future-route", []string{"calendar.write"}, false},
		{http.MethodPost, "calendar/events/event_1/unknown", []string{"calendar.write"}, false},
	}
	for _, test := range tests {
		session := db.AppRuntimeSession{AppID: "planner", SpaceID: "space_1", Scopes: test.scopes}
		path := "/v1/spaces/space_1/" + test.path
		if got := TestingAuthorizeAppRuntimeRequest(session, test.method, path); got != test.allowed {
			t.Errorf("%s %s with %v = %v, want %v", test.method, path, test.scopes, got, test.allowed)
		}
		for _, otherSpace := range []string{"space_2", ""} {
			session.SpaceID = otherSpace
			if TestingAuthorizeAppRuntimeRequest(session, test.method, path) {
				t.Errorf("%s %s escaped session Space %q", test.method, path, otherSpace)
			}
		}
	}
}

func TestAppStorageRequiresReadAndWriteGrants(t *testing.T) {
	for _, scope := range []string{"", "storage.read", "storage.write"} {
		session := db.AppRuntimeSession{AppID: "planner", Scopes: []string{scope}}
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			path, expectedScope := "/v1/app-runtime/records", "storage.read"
			if method != http.MethodGet {
				path += "/key"
				expectedScope = "storage.write"
			}
			if got := TestingAuthorizeAppRuntimeRequest(session, method, path); got != (scope == expectedScope) {
				t.Errorf("%s %s with %q = %v", method, path, scope, got)
			}
		}
	}
}

func TestAppRuntimeAuthorizationSupportsExplicitAccountCapabilities(t *testing.T) {
	session := db.AppRuntimeSession{
		AppID:  "inbox",
		Scopes: []string{"mail.read", "mail.write", "connections.read", "profile.read", "ai.read", "ai.write", "activity.read", "activity.write"},
	}
	tests := []struct {
		method, path string
		allowed      bool
	}{
		{http.MethodGet, "/v1/mail/threads", true},
		{http.MethodPost, "/v1/mail/drafts", true},
		{http.MethodDelete, "/v1/mail/threads/thread_1", true},
		{http.MethodGet, "/v1/cloud/connections", true},
		{http.MethodDelete, "/v1/cloud/connections/cloud_1", false},
		{http.MethodGet, "/v1/me", true},
		{http.MethodPut, "/v1/me/profile", false},
		{http.MethodPost, "/v1/ai/complete", true},
		{http.MethodGet, "/v1/misty/conversations", true},
		{http.MethodPost, "/v1/misty/conversations", true},
		{http.MethodGet, "/v1/activity/inbox", true},
		{http.MethodPost, "/v1/activity/inbox/seen", true},
		{http.MethodGet, "/v1/agents", false},
		{http.MethodGet, "/v1/apps", false},
	}
	for _, test := range tests {
		if got := TestingAuthorizeAppRuntimeRequest(session, test.method, test.path); got != test.allowed {
			t.Errorf("authorize(%s %s) = %v, want %v", test.method, test.path, got, test.allowed)
		}
	}
}
