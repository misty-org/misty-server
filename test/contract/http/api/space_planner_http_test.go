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

func plannerJSON(t *testing.T, response *httptest.ResponseRecorder, want int) map[string]any {
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

// A Space's board over the routes the app calls: tasks are created, edited,
// moved between and within columns, and archived, and only members reach them.
func TestSpaceTaskRoutesKeepTheBoardInOrder(t *testing.T) {
	database := openPresenceTestDatabase(t)
	ctx := context.Background()
	spaces, err := NewSpacesService(database, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Get("/spaces/{spaceID}/tasks", spaces.SpaceTasks())
	router.Post("/spaces/{spaceID}/tasks", spaces.SpaceTasks())
	router.Patch("/spaces/{spaceID}/tasks/{taskID}", spaces.SpaceTask())
	router.Delete("/spaces/{spaceID}/tasks/{taskID}", spaces.SpaceTask())
	router.Post("/spaces/{spaceID}/tasks/{taskID}/move", spaces.MoveSpaceTask())

	owner, err := database.CreateUser(uniqueTestEmail("planner-owner"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := database.CreateUser(uniqueTestEmail("planner-outsider"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Planner")
	if err != nil {
		t.Fatal(err)
	}
	token := newConversationTestBearerToken(t, database, owner.ID)
	outsiderToken := newConversationTestBearerToken(t, database, outsider.ID)
	path := "/spaces/" + space.ID + "/tasks"
	call := func(method, target string, body any) *httptest.ResponseRecorder {
		return performConversationRequest(t, router, method, target, token, body)
	}

	ids := []string{}
	for _, title := range []string{"First", "Second", "Third"} {
		task := plannerJSON(t, call(http.MethodPost, path, map[string]any{"title": title}), http.StatusCreated)
		if task["status"] != "todo" || task["priority"] != "medium" || task["task_number"] != float64(len(ids)+1) {
			t.Fatalf("created task = %v", task)
		}
		ids = append(ids, task["id"].(string))
	}
	order := func(status string) []string {
		listed := plannerJSON(t, call(http.MethodGet, path, nil), http.StatusOK)
		titles := []string{}
		for _, item := range listed["tasks"].([]any) {
			task := item.(map[string]any)
			if task["status"] == status {
				titles = append(titles, task["title"].(string))
			}
		}
		return titles
	}
	if got := strings.Join(order("todo"), ","); got != "First,Second,Third" {
		t.Fatalf("board order = %s", got)
	}

	// Third goes before First; then Second moves to In progress.
	plannerJSON(t, call(http.MethodPost, path+"/"+ids[2]+"/move", map[string]any{"status": "todo", "before_task_id": ids[0]}), http.StatusOK)
	if got := strings.Join(order("todo"), ","); got != "Third,First,Second" {
		t.Fatalf("after reorder = %s", got)
	}
	moved := plannerJSON(t, call(http.MethodPost, path+"/"+ids[1]+"/move", map[string]any{"status": "in_progress"}), http.StatusOK)
	if moved["task"].(map[string]any)["status"] != "in_progress" {
		t.Fatalf("moved = %v", moved)
	}
	// Dropping repeatedly into the same gap respaces the column instead of colliding.
	for i := 0; i < 14; i++ {
		target := ids[i%2*2]
		before := ids[2-i%2*2]
		plannerJSON(t, call(http.MethodPost, path+"/"+target+"/move", map[string]any{"status": "todo", "before_task_id": before}), http.StatusOK)
	}
	if got := strings.Join(order("todo"), ","); got != "Third,First" {
		t.Fatalf("after repeated drops = %s", got)
	}

	edited := plannerJSON(t, call(http.MethodPatch, path+"/"+ids[0], map[string]any{
		"title": "First, renamed", "priority": "high", "due_at": "2026-12-01T09:00:00Z",
		"assignee_user_id": owner.ID, "status": "done",
	}), http.StatusOK)
	if edited["title"] != "First, renamed" || edited["priority"] != "high" || edited["assignee_user_id"] != owner.ID ||
		edited["status"] != "done" || edited["completed_at"] == nil || edited["due_at"] == nil {
		t.Fatalf("edited = %v", edited)
	}
	cleared := plannerJSON(t, call(http.MethodPatch, path+"/"+ids[0], map[string]any{"due_at": "", "assignee_agent_id": "agent-on-my-computer"}), http.StatusOK)
	if cleared["due_at"] != nil || cleared["assignee_user_id"] != nil || cleared["assignee_agent_id"] != "agent-on-my-computer" || cleared["title"] != "First, renamed" {
		t.Fatalf("cleared = %v", cleared)
	}

	for name, body := range map[string]map[string]any{
		"no title":         {"title": " "},
		"unknown status":   {"title": "x", "status": "blocked"},
		"outside assignee": {"title": "x", "assignee_user_id": outsider.ID},
	} {
		if got := call(http.MethodPost, path, body); got.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d %s", name, got.Code, got.Body.String())
		}
	}
	if got := performConversationRequest(t, router, http.MethodGet, path, outsiderToken, nil); got.Code != http.StatusForbidden {
		t.Fatalf("outsider list = %d", got.Code)
	}
	if got := performConversationRequest(t, router, http.MethodPost, path, outsiderToken, map[string]any{"title": "Mine"}); got.Code != http.StatusForbidden {
		t.Fatalf("outsider create = %d", got.Code)
	}

	for range 2 {
		plannerJSON(t, call(http.MethodDelete, path+"/"+ids[2], nil), http.StatusNoContent)
	}
	if got := strings.Join(order("todo"), ","); got != "" {
		t.Fatalf("after archive = %s", got)
	}
	if got := call(http.MethodDelete, path+"/task_missing", nil); got.Code != http.StatusNotFound {
		t.Fatalf("archive missing = %d", got.Code)
	}
	var events int
	if err := database.Conn.QueryRow(`SELECT count(*) FROM space_events WHERE space_id=$1 AND event_type LIKE 'task.%'`, space.ID).Scan(&events); err != nil || events < 6 {
		t.Fatalf("task events = %d %v", events, err)
	}
}

// A Space's calendar: events are created, listed by range, edited and deleted.
func TestSpaceCalendarEventRoutes(t *testing.T) {
	database := openPresenceTestDatabase(t)
	ctx := context.Background()
	spaces, err := NewSpacesService(database, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Get("/spaces/{spaceID}/calendar/events", spaces.SpaceCalendarEvents())
	router.Post("/spaces/{spaceID}/calendar/events", spaces.SpaceCalendarEvents())
	router.Patch("/spaces/{spaceID}/calendar/events/{eventID}", spaces.SpaceCalendarEvent())
	router.Delete("/spaces/{spaceID}/calendar/events/{eventID}", spaces.SpaceCalendarEvent())

	owner, err := database.CreateUser(uniqueTestEmail("calendar-owner"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := database.CreateUser(uniqueTestEmail("calendar-outsider"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Calendar")
	if err != nil {
		t.Fatal(err)
	}
	token := newConversationTestBearerToken(t, database, owner.ID)
	path := "/spaces/" + space.ID + "/calendar/events"
	call := func(method, target string, body any) *httptest.ResponseRecorder {
		return performConversationRequest(t, router, method, target, token, body)
	}
	body := map[string]any{
		"title": "Planning", "description": "Quarter goals", "location": "Room 2",
		"starts_at": "2026-12-01T17:00:00Z", "ends_at": "2026-12-01T18:00:00Z",
		"all_day": false, "timezone": "America/Los_Angeles",
	}
	event := plannerJSON(t, call(http.MethodPost, path, body), http.StatusCreated)
	id := event["id"].(string)
	if event["title"] != "Planning" || event["timezone"] != "America/Los_Angeles" || event["created_by_user_id"] != owner.ID {
		t.Fatalf("created = %v", event)
	}
	count := func(query string) int {
		listed := plannerJSON(t, call(http.MethodGet, path+query, nil), http.StatusOK)
		return len(listed["events"].([]any))
	}
	if count("?from=2026-12-01T00:00:00Z&to=2026-12-02T00:00:00Z") != 1 || count("?from=2026-12-02T00:00:00Z&to=2026-12-03T00:00:00Z") != 0 {
		t.Fatal("the range does not select the event's day")
	}
	if got := call(http.MethodGet, path, nil); got.Code != http.StatusBadRequest {
		t.Fatalf("list without a range = %d", got.Code)
	}

	body["title"], body["starts_at"], body["ends_at"] = "Planning, moved", "2026-12-02T17:00:00Z", "2026-12-02T18:00:00Z"
	updated := plannerJSON(t, call(http.MethodPatch, path+"/"+id, body), http.StatusOK)
	if updated["title"] != "Planning, moved" || updated["version"] != float64(2) {
		t.Fatalf("updated = %v", updated)
	}
	body["ends_at"] = "2026-12-02T16:00:00Z"
	if got := call(http.MethodPatch, path+"/"+id, body); got.Code != http.StatusBadRequest {
		t.Fatalf("an event that ends before it starts = %d", got.Code)
	}
	body["ends_at"], body["timezone"] = "2026-12-02T18:00:00Z", "Mars/Olympus"
	if got := call(http.MethodPost, path, body); got.Code != http.StatusBadRequest {
		t.Fatalf("an unknown timezone = %d", got.Code)
	}
	outsiderToken := newConversationTestBearerToken(t, database, outsider.ID)
	if got := performConversationRequest(t, router, http.MethodDelete, path+"/"+id, outsiderToken, nil); got.Code != http.StatusForbidden {
		t.Fatalf("outsider delete = %d", got.Code)
	}
	plannerJSON(t, call(http.MethodDelete, path+"/"+id, nil), http.StatusNoContent)
	if got := call(http.MethodDelete, path+"/"+id, nil); got.Code != http.StatusNotFound {
		t.Fatalf("delete twice = %d", got.Code)
	}
}
