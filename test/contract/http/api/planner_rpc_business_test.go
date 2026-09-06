package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/appcatalog"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// This uses the same public RPC envelope, reviewed App grant and real domain
// handlers as the downloaded Planner. Keep it as a parity gate for Hono.
func TestPlannerRPCBusinessPaths(t *testing.T) {
	database := openPresenceTestDatabase(t)
	user, err := database.CreateUserWithUsername("Planner RPC", "planner_"+uuid.NewString()[:8], uniqueTestEmail("planner-rpc"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(t.Context(), user.ID, "Planner RPC Space")
	if err != nil {
		t.Fatal(err)
	}
	catalog, ok := appcatalog.Find("planner")
	if !ok {
		t.Fatal("Planner catalog missing")
	}
	if _, err := database.InstallUserApp(t.Context(), user.ID, "planner", catalog.Version, catalog.PermissionVersion, catalog.Scopes); err != nil {
		t.Fatal(err)
	}
	token := "planner-rpc-" + uuid.NewString()
	if _, err := database.CreateAppRuntimeSession(t.Context(), user.ID, "planner", security.HashToken(token), space.ID, db.AppRuntimeSessionTTL); err != nil {
		t.Fatal(err)
	}
	spaces, err := NewSpacesService(database, nil, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Get("/spaces/{spaceID}/tasks", spaces.SpaceTasks())
	router.Post("/spaces/{spaceID}/tasks", spaces.SpaceTasks())
	router.Patch("/spaces/{spaceID}/tasks/{taskID}", spaces.SpaceTask())
	router.Delete("/spaces/{spaceID}/tasks/{taskID}", spaces.SpaceTask())
	router.Post("/spaces/{spaceID}/tasks/{taskID}/move", spaces.MoveSpaceTask())
	router.Get("/spaces/{spaceID}/calendar/events", spaces.SpaceCalendar())
	router.Post("/spaces/{spaceID}/calendar/events", spaces.SpaceCalendar())
	router.Patch("/spaces/{spaceID}/calendar/events/{eventID}", spaces.SpaceNativeCalendarEvent())
	router.Delete("/spaces/{spaceID}/calendar/events/{eventID}", spaces.SpaceNativeCalendarEvent())
	router.Post("/spaces/{spaceID}/roadmaps", spaces.SpaceRoadmaps())
	router.Get("/spaces/{spaceID}/roadmaps/{roadmapID}", spaces.SpaceRoadmap())
	router.Post("/spaces/{spaceID}/roadmaps/{roadmapID}/nodes", spaces.SpaceRoadmapNodes())
	router.Patch("/spaces/{spaceID}/roadmaps/{roadmapID}/nodes/{nodeID}", spaces.SpaceRoadmapNode())
	router.Patch("/spaces/{spaceID}/roadmaps/{roadmapID}/layout", spaces.SpaceRoadmapLayout())
	router.Post("/app-runtime/rpc", OfficialAppRPC(database, router, ""))
	rpc := func(t *testing.T) func(string, map[string]string, any, any, int) map[string]any {
		return func(method string, path map[string]string, body, query any, status int) map[string]any {
			t.Helper()
			params := map[string]any{}
			if path != nil {
				params["path"] = path
			}
			if body != nil {
				params["body"] = body
			}
			if query != nil {
				params["query"] = query
			}
			response := performConversationRequest(t, router, http.MethodPost, "/app-runtime/rpc", token, map[string]any{"protocol": 2, "method": method, "params": params})
			if response.Code != status {
				t.Fatalf("%s = %d, want %d: %s", method, response.Code, status, response.Body.String())
			}
			var result map[string]any
			if response.Body.Len() > 0 && json.Unmarshal(response.Body.Bytes(), &result) != nil && status < 400 {
				t.Fatalf("%s returned invalid JSON: %s", method, response.Body.String())
			}
			return result
		}
	}

	t.Run("task create list update move archive and last-write-wins", func(t *testing.T) {
		call := rpc(t)
		task := call("tasks.create", nil, map[string]any{"title": "RPC task", "priority": "high"}, nil, 201)
		path := map[string]string{"taskID": task["id"].(string)}
		page := call("tasks.list", nil, nil, map[string]any{"q": "RPC task", "limit": 10}, 200)
		if len(page["tasks"].([]any)) != 1 {
			t.Fatalf("task list = %#v", page)
		}
		task["title"] = "RPC task edited"
		updated := call("tasks.update", path, task, nil, 200)
		if updated["title"] != task["title"] || updated["version"].(float64) <= task["version"].(float64) {
			t.Fatalf("task update = %#v", updated)
		}
		updated = call("tasks.update", path, task, nil, 200)
		move := map[string]any{"version": updated["version"], "status": "in_progress"}
		moved := call("tasks.move", path, move, nil, 200)["task"].(map[string]any)
		if moved["status"] != "in_progress" {
			t.Fatalf("move = %#v", moved)
		}
		moved = call("tasks.move", path, move, nil, 200)["task"].(map[string]any)
		archived := call("tasks.delete", path, nil, map[string]any{"version": moved["version"]}, 200)
		if archived["archived_at"] == nil {
			t.Fatalf("not archived: %#v", archived)
		}
		call("tasks.update", path, task, nil, 404)
		call("tasks.move", path, move, nil, 404)
		retried := call("tasks.delete", path, nil, map[string]any{"version": moved["version"]}, 200)
		if retried["version"] != archived["version"] {
			t.Fatal("archive retry changed version")
		}
		page = call("tasks.list", nil, nil, map[string]any{"q": "RPC task"}, 200)
		if len(page["tasks"].([]any)) != 0 {
			t.Fatalf("archived task visible: %#v", page)
		}
		page = call("tasks.list", nil, nil, map[string]any{"q": "RPC task", "include_archived": true}, 200)
		if len(page["tasks"].([]any)) != 1 {
			t.Fatalf("archived task lost: %#v", page)
		}
	})
	t.Run("calendar create update delete and stale versions", func(t *testing.T) {
		call := rpc(t)
		event := call("calendar.events.create", nil, map[string]any{"title": "RPC event", "starts_at": "2026-09-05T12:00:00Z", "ends_at": "2026-09-05T13:00:00Z", "timezone": "UTC"}, nil, 201)
		path := map[string]string{"eventID": event["id"].(string)}
		event["title"] = "RPC event edited"
		updated := call("calendar.events.update", path, event, nil, 200)
		if updated["title"] != event["title"] {
			t.Fatalf("event update = %#v", updated)
		}
		call("calendar.events.update", path, event, nil, 409)
		query := map[string]any{"from": "2026-09-05T00:00:00Z", "to": "2026-09-06T00:00:00Z"}
		if len(call("calendar.events.list", nil, nil, query, 200)["events"].([]any)) != 1 {
			t.Fatal("event missing")
		}
		call("calendar.events.delete", path, nil, map[string]any{"version": event["version"]}, 409)
		call("calendar.events.delete", path, nil, map[string]any{"version": updated["version"]}, 204)
		if len(call("calendar.events.list", nil, nil, query, 200)["events"].([]any)) != 0 {
			t.Fatal("deleted event visible")
		}
	})
	t.Run("roadmap versioned node and layout edits reject stale writers", func(t *testing.T) {
		call := rpc(t)
		roadmap := call("roadmaps.create", nil, map[string]any{"name": "RPC roadmap"}, nil, 201)["roadmap"].(map[string]any)
		path := map[string]string{"roadmapID": roadmap["id"].(string)}
		input := map[string]any{"title": "RPC note", "node_kind": "note", "position_x": 10, "position_y": 20, "expected_version": roadmap["graph_version"]}
		created := call("roadmaps.nodes.create", path, input, nil, 201)
		call("roadmaps.nodes.create", path, input, nil, 409)
		node := created["node"].(map[string]any)
		node["title"], node["expected_version"] = "RPC note edited", created["graph_version"]
		nodePath := map[string]string{"roadmapID": path["roadmapID"], "nodeID": node["id"].(string)}
		updated := call("roadmaps.nodes.update", nodePath, node, nil, 200)
		call("roadmaps.nodes.update", nodePath, node, nil, 409)
		layout := map[string]any{"expected_version": updated["graph_version"], "nodes": []any{map[string]any{"id": node["id"], "position_x": 125, "position_y": 250}}}
		placed := call("roadmaps.layout.update", path, layout, nil, 200)
		call("roadmaps.layout.update", path, layout, nil, 409)
		snapshot := call("roadmaps.get", path, nil, nil, 200)
		if snapshot["roadmap"].(map[string]any)["graph_version"] != placed["graph_version"] {
			t.Fatalf("graph version = %#v", snapshot)
		}
		nodes := snapshot["nodes"].([]any)
		if len(nodes) != 1 {
			t.Fatalf("stale writer created another node: %#v", nodes)
		}
		persisted := nodes[0].(map[string]any)
		if persisted["title"] != "RPC note edited" || persisted["position_x"] != float64(125) || persisted["position_y"] != float64(250) {
			t.Fatalf("persisted node = %#v", persisted)
		}
	})
	t.Run("bound Space and reviewed scopes remain mandatory", func(t *testing.T) {
		call := rpc(t)
		call("tasks.create", map[string]string{"spaceID": "another-space"}, map[string]any{"title": "must fail"}, nil, 400)
		if _, err := database.InstallUserApp(t.Context(), user.ID, "planner", catalog.Version, catalog.PermissionVersion, []string{"tasks.read"}); err != nil {
			t.Fatal(err)
		}
		token = "planner-rpc-readonly-" + uuid.NewString()
		if _, err := database.CreateAppRuntimeSession(t.Context(), user.ID, "planner", security.HashToken(token), space.ID, db.AppRuntimeSessionTTL); err != nil {
			t.Fatal(err)
		}
		call("tasks.list", nil, nil, nil, 200)
		call("tasks.create", nil, map[string]any{"title": "must fail"}, nil, 403)
		call("calendar.events.create", nil, map[string]any{"title": "must fail"}, nil, 403)
		call("roadmaps.create", nil, map[string]any{"name": "must fail"}, nil, 403)
	})
}
