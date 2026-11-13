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

// Bring an agent, over the routes the app calls: a Space lists an agent from the
// id and name the caller's computer sends, with no agent row on the server.
func TestSpaceAgentRoutesListAnAgentByNameOnly(t *testing.T) {
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

	put := performConversationRequest(t, router, http.MethodPut, path+"/agent-on-my-computer", ownerToken, map[string]string{"name": "Researcher"})
	if put.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", put.Code, put.Body.String())
	}
	var listed map[string]any
	if err := json.Unmarshal(put.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if listed["agent_id"] != "agent-on-my-computer" || listed["agent_name"] != "Researcher" || listed["owner_user_id"] != owner.ID || listed["space_name"] != "Agents" {
		t.Fatalf("listing = %v", listed)
	}
	for _, retired := range []string{"agent_role", "agent_avatar", "system_managed"} {
		if _, kept := listed[retired]; kept {
			t.Fatalf("a Space knows more than the agent's name: %v", listed)
		}
	}
	for _, read := range []string{path, "/me/space-agents"} {
		got := performConversationRequest(t, router, http.MethodGet, read, ownerToken, nil)
		if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "agent-on-my-computer") {
			t.Fatalf("GET %s = %d %s", read, got.Code, got.Body.String())
		}
	}
	if got := performConversationRequest(t, router, http.MethodPut, path+"/no-name", ownerToken, map[string]string{"name": " "}); got.Code/100 != 4 {
		t.Fatalf("a listing with no name = %d", got.Code)
	}
	if got := performConversationRequest(t, router, http.MethodPut, path+"/theirs", outsiderToken, map[string]string{"name": "Mine"}); got.Code/100 != 4 {
		t.Fatalf("listing an agent in someone else's Space = %d", got.Code)
	}
	if got := performConversationRequest(t, router, http.MethodDelete, path+"/agent-on-my-computer", ownerToken, nil); got.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", got.Code, got.Body.String())
	}
	if got := performConversationRequest(t, router, http.MethodGet, path, ownerToken, nil); strings.Contains(got.Body.String(), "agent-on-my-computer") {
		t.Fatalf("still listed: %s", got.Body.String())
	}
}
