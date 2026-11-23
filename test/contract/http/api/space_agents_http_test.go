package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	. "github.com/misty-org/misty-server/internal/platform/httpapi"
)

func TestSpaceAgentRoutesKeepTheAgentsDefinition(t *testing.T) {
	database := openPresenceTestDatabase(t)
	ctx := context.Background()
	spaces, err := NewSpacesService(database, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Get("/spaces/{spaceID}/agents", spaces.SpaceAgents())
	router.Put("/spaces/{spaceID}/agents/{agentID}", spaces.SpaceAgent())
	router.Delete("/spaces/{spaceID}/agents/{agentID}", spaces.SpaceAgent())
	router.Get("/me/space-agents", spaces.MySpaceAgents())

	owner, err := database.CreateUser(uniqueTestEmail("agent-owner"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := database.CreateUser(uniqueTestEmail("agent-outsider"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.TestingCreateSpace(ctx, owner.ID, "Agents")
	if err != nil {
		t.Fatal(err)
	}
	ownerToken := newConversationTestBearerToken(t, database, owner.ID)
	outsiderToken := newConversationTestBearerToken(t, database, outsider.ID)
	path := "/spaces/" + space.ID + "/agents"

	definition := map[string]any{
		"name": "Researcher", "instructions": "Check the sources.", "voice_id": "calm-1",
		"engine_id": "claude", "avatar": map[string]string{"cloudVariant": "mint"},
	}
	put := performConversationRequest(t, router, http.MethodPut, path+"/agent-on-my-computer", ownerToken, definition)
	if put.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", put.Code, put.Body.String())
	}
	var listed map[string]any
	if err := json.Unmarshal(put.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if listed["agent_id"] != "agent-on-my-computer" || listed["agent_name"] != "Researcher" || listed["owner_user_id"] != owner.ID ||
		listed["space_name"] != "Agents" || listed["instructions"] != "Check the sources." || listed["engine_id"] != "claude" ||
		listed["voice_id"] != "calm-1" || listed["revision"] != float64(1) || listed["can_edit"] != true {
		t.Fatalf("definition = %v", listed)
	}
	for _, kept := range []string{"enabled", "default_run_mode", "tools", "mcp_servers", "runtime_cwd", "runtime_args", "memory", "connectors", "api_key"} {
		if _, sent := listed[kept]; sent {
			t.Fatalf("a Space holds what stays on the person's computer (%s): %v", kept, listed)
		}
	}
	for _, private := range []map[string]any{
		{"name": "Researcher", "revision": 1, "mcp_servers": []string{"github"}},
		{"name": "Researcher", "revision": 1, "api_key": "sk-123"},
		{"name": "Researcher", "revision": 1, "memory": "notes"},
		{"name": "Researcher", "revision": 1, "connectors": []string{"gmail"}},
		{"name": "Researcher", "revision": 1, "prompt": "hello"},
	} {
		if got := performConversationRequest(t, router, http.MethodPut, path+"/agent-on-my-computer", ownerToken, private); got.Code != http.StatusBadRequest {
			t.Fatalf("PUT with %v = %d", private, got.Code)
		}
	}
	if stale := performConversationRequest(t, router, http.MethodPut, path+"/agent-on-my-computer", ownerToken, map[string]any{"name": "Old"}); stale.Code != http.StatusConflict {
		t.Fatalf("an edit from an old revision = %d %s", stale.Code, stale.Body.String())
	}
	for _, read := range []string{path, "/me/space-agents"} {
		got := performConversationRequest(t, router, http.MethodGet, read, ownerToken, nil)
		if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "agent-on-my-computer") {
			t.Fatalf("GET %s = %d %s", read, got.Code, got.Body.String())
		}
	}
	if got := performConversationRequest(t, router, http.MethodPut, path+"/no-name", ownerToken, map[string]string{"name": " "}); got.Code/100 != 4 {
		t.Fatalf("an agent with no name = %d", got.Code)
	}
	if got := performConversationRequest(t, router, http.MethodPut, path+"/theirs", outsiderToken, map[string]string{"name": "Mine"}); got.Code/100 != 4 {
		t.Fatalf("defining an agent in someone else's Space = %d", got.Code)
	}
	if got := performConversationRequest(t, router, http.MethodDelete, path+"/agent-on-my-computer", ownerToken, nil); got.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", got.Code, got.Body.String())
	}
	if got := performConversationRequest(t, router, http.MethodGet, path, ownerToken, nil); strings.Contains(got.Body.String(), "agent-on-my-computer") {
		t.Fatalf("still listed: %s", got.Body.String())
	}
}
