package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/misty-org/misty-server/internal/platform/db"
)

func agentThreadRoutes(f *spaceRouteFixture) {
	service := &SpacesService{database: f.database}
	f.router.Get("/spaces/{spaceID}/agents/{agentID}/threads", service.AgentThreads())
	f.router.Put("/spaces/{spaceID}/agent-threads/{threadID}", service.AgentThread())
	f.router.Delete("/spaces/{spaceID}/agent-threads/{threadID}", service.AgentThread())
	f.router.Get("/spaces/{spaceID}/agent-threads/{threadID}/entries", service.AgentThreadEntries())
	f.router.Post("/spaces/{spaceID}/agent-threads/{threadID}/entries", service.AgentThreadEntries())
	f.router.Post("/spaces/{spaceID}/agent-threads/{threadID}/reactions", service.AgentThreadReaction())
}

func TestAgentThreadsAreReadByMembersAndWrittenByTheirPersonPostgres(t *testing.T) {
	f := newSpaceRouteFixture(t)
	agentThreadRoutes(f)
	if _, err := f.database.PutSpaceAgent(t.Context(), f.member, f.space.ID, "personal_ada",
		db.SpaceAgentDefinition{Name: "Ada", EngineID: "claude"}); err != nil {
		t.Fatal(err)
	}
	base := "/spaces/" + f.space.ID
	thread := base + "/agent-threads/chat_one"

	if got := f.do(f.member, http.MethodPut, thread, map[string]any{
		"agent_id": "personal_ada", "title": "Launch plan", "state": "working",
	}); got.Code != http.StatusOK {
		t.Fatalf("member starts a thread: %d %s", got.Code, got.Body.String())
	}
	if got := f.do(f.other, http.MethodPut, thread, map[string]any{
		"agent_id": "personal_ada", "title": "Taken over", "state": "resting",
	}); got.Code == http.StatusOK {
		t.Fatalf("someone else wrote in the thread: %s", got.Body.String())
	}
	if got := f.do(f.member, http.MethodPost, thread+"/entries", map[string]any{
		"entries": []map[string]any{
			{"key": "m1:ask", "kind": "ask", "body": "Draft the launch plan"},
			{"key": "m2:steps", "kind": "step", "body": "Edited plan.md"},
			{"key": "m2:reply", "kind": "reply", "body": "Here is the plan."},
		},
	}); got.Code != http.StatusOK {
		t.Fatalf("member adds entries: %d %s", got.Code, got.Body.String())
	}
	if got := f.do(f.other, http.MethodPost, thread+"/entries", map[string]any{
		"entries": []map[string]any{{"key": "x:ask", "kind": "ask", "body": "Do something else"}},
	}); got.Code == http.StatusOK {
		t.Fatalf("someone else added to the thread: %s", got.Body.String())
	}
	if got := f.do(f.member, http.MethodPost, thread+"/entries", map[string]any{
		"entries": []map[string]any{{"key": "m3:tool", "kind": "tool_result", "body": "secret output"}},
	}); got.Code == http.StatusOK {
		t.Fatalf("a raw tool result was kept: %s", got.Body.String())
	}

	if got := f.ids(f.do(f.other, http.MethodGet, base+"/agents/personal_ada/threads", nil), "threads"); !sameIDs(got, "chat_one") {
		t.Fatalf("another member's list = %v", got)
	}
	if got := f.do(f.stranger, http.MethodGet, base+"/agents/personal_ada/threads", nil); got.Code == http.StatusOK {
		t.Fatalf("a stranger read the threads: %s", got.Body.String())
	}

	if got := f.do(f.other, http.MethodPost, thread+"/reactions", map[string]any{
		"key": "m2:reply", "emoji": "👍", "on": true,
	}); got.Code != http.StatusNoContent {
		t.Fatalf("member reacts: %d %s", got.Code, got.Body.String())
	}
	read := f.do(f.other, http.MethodGet, thread+"/entries", nil)
	if read.Code != http.StatusOK {
		t.Fatalf("entries: %d %s", read.Code, read.Body.String())
	}
	var body struct {
		Entries []db.AgentThreadEntry `json:"entries"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Entries) != 3 || body.Entries[0].Key != "m1:ask" || body.Entries[2].Key != "m2:reply" {
		t.Fatalf("entries = %+v", body.Entries)
	}
	if reactions := body.Entries[2].Reactions; len(reactions) != 1 || reactions[0].Count != 1 || !reactions[0].Mine {
		t.Fatalf("reactions = %+v", reactions)
	}

	if got := f.do(f.other, http.MethodDelete, thread, nil); got.Code == http.StatusNoContent {
		t.Fatalf("a member without moderation removed someone's thread")
	}
	if got := f.do(f.owner, http.MethodDelete, thread, nil); got.Code != http.StatusNoContent {
		t.Fatalf("the owner removes the thread: %d %s", got.Code, got.Body.String())
	}
}

func TestAgentThreadsStayOffPersonalSpacesPostgres(t *testing.T) {
	f := newSpaceRouteFixture(t)
	agentThreadRoutes(f)
	var personal string
	if err := f.database.SpaceTx(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), `SELECT id FROM spaces WHERE owner_user_id=$1 AND is_default`, f.owner).Scan(&personal)
	}); err != nil {
		t.Skip("no personal Space in this fixture")
	}
	got := f.do(f.owner, http.MethodPut, "/spaces/"+personal+"/agent-threads/chat_mine", map[string]any{
		"agent_id": "personal_ada", "title": "Private", "state": "working",
	})
	if got.Code == http.StatusOK {
		t.Fatalf("a Personal thread went to the server: %s", got.Body.String())
	}
}
