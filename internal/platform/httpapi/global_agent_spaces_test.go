package api

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	agent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/test/testkit"
)

func TestGlobalAgentSpacesPostgres(t *testing.T) {
	if os.Getenv("TEST_DB_NAME") != "misty_global_agents_test" {
		t.Skip("requires isolated global agents test database")
	}
	database := testkit.OpenDatabase(t)
	ctx := t.Context()
	owner, err := database.CreateUser("Global owner", "global-owner@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateUser("Other owner", "global-other@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	origin, err := database.CreateSpace(ctx, owner.ID, "Work")
	if err != nil {
		t.Fatal(err)
	}
	destination, err := database.CreateSpace(ctx, owner.ID, "Family")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := database.CreateSpace(ctx, other.ID, "Private")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := database.SavePersonalAgent(ctx, owner.ID, "", db.AgentProfileInput{Name: "Editor", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := database.CreatePersonalAgentConversation(ctx, owner.ID, origin.ID, identity.ID)
	if err != nil {
		t.Fatal(err)
	}
	payload := TestingMustAPIRawJSON(map[string]string{"agent_id": identity.ID, "execution_mode": "user", "prompt": "Create a task in Family"})
	record, _, err := database.CreateAIInvocationRecord(ctx, db.AIInvocationRecord{ID: "invocation_global_test", UserID: owner.ID, SpaceID: origin.ID, Mode: "drawer", SurfaceID: "global", Trigger: "message", State: "running", IdempotencyKey: "global-test", RequestPayload: payload, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	invocation := agenttools.Invocation{UserID: owner.ID, SpaceID: origin.ID, AgentID: identity.ID, RunID: record.ID, SessionID: conversation, Source: "space_conversation", Trigger: "message", OriginalInput: "Create a task in Family"}
	list, err := executeGlobalAgentSpaceTool(ctx, database, invocation, agent.ToolRequest{Name: "spaces.list", Arguments: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(list), destination.ID) || strings.Contains(string(list), foreign.ID) {
		t.Fatalf("space discovery: %s", list)
	}
	catalog, err := executeGlobalAgentSpaceTool(ctx, database, invocation, agent.ToolRequest{Name: "spaces.tools", Arguments: TestingMustAPIRawJSON(map[string]string{"space_id": destination.ID})})
	if err != nil || !strings.Contains(string(catalog), "tasks.create") {
		t.Fatalf("destination tools: %s %v", catalog, err)
	}
	request := agent.ToolRequest{ID: "create-one", Name: "spaces.execute", Arguments: TestingMustAPIRawJSON(map[string]any{"space_id": destination.ID, "tool": "tasks.create", "arguments": map[string]string{"title": "Book dinner"}})}
	result, err := executeGlobalAgentSpaceTool(ctx, database, invocation, request)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := executeGlobalAgentSpaceTool(ctx, database, invocation, request)
	var originalValue, replayValue any
	_ = json.Unmarshal(result, &originalValue)
	_ = json.Unmarshal(replay, &replayValue)
	if err != nil || !reflect.DeepEqual(originalValue, replayValue) {
		t.Fatalf("replay: %s %v", replay, err)
	}
	request.Arguments = TestingMustAPIRawJSON(map[string]any{"space_id": origin.ID, "tool": "tasks.create", "arguments": map[string]string{"title": "Book dinner"}})
	if _, err = executeGlobalAgentSpaceTool(ctx, database, invocation, request); err == nil {
		t.Fatal("same call ID silently changed destination")
	}
	request.Arguments = TestingMustAPIRawJSON(map[string]any{"space_id": foreign.ID, "tool": "tasks.query", "arguments": map[string]string{}})
	if _, err = executeGlobalAgentSpaceTool(ctx, database, invocation, request); err == nil {
		t.Fatal("foreign space accessible")
	}
	request.Arguments = TestingMustAPIRawJSON(map[string]any{"space_id": destination.ID, "tool": "spaces.execute", "arguments": map[string]string{}})
	if _, err = executeGlobalAgentSpaceTool(ctx, database, invocation, request); err == nil {
		t.Fatal("nested routing allowed")
	}
	catalog, err = executeGlobalAgentSpaceTool(ctx, database, invocation, agent.ToolRequest{Name: "spaces.tools", Arguments: TestingMustAPIRawJSON(map[string]string{"space_id": destination.ID})})
	if err != nil || !strings.Contains(string(catalog), "tasks.create") {
		t.Fatalf("first-party tools require no app installation: %s %v", catalog, err)
	}
}
