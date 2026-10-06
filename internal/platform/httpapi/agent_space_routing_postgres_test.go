package api

import (
	"encoding/json"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/test/testkit"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	agent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)


import ()

func TestRoutedSpaceToolsPostgres(t *testing.T) {
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
	origin, err := database.TestingCreateSpace(ctx, owner.ID, "Work")
	if err != nil {
		t.Fatal(err)
	}
	destination, err := database.TestingCreateSpace(ctx, owner.ID, "Family")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := database.TestingCreateSpace(ctx, other.ID, "Private")
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
	// Account runs reach every Space through the same tool names.
	invocation := agenttools.Invocation{UserID: owner.ID, AgentID: identity.ID, RunID: record.ID, SessionID: conversation, Source: "space_conversation", Trigger: "message", OriginalInput: "Create a task in Family"}
	toolbox := buildAgentToolbox(database, agentToolboxOptions{accountLevel: true})
	run := func(id, name string, arguments any) (json.RawMessage, error) {
		return executeSpaceAgentToolbox(ctx, toolbox, invocation, database, agent.ToolRequest{ID: id, Name: name, Arguments: TestingMustAPIRawJSON(arguments)})
	}
	list, err := run("list", "spaces.list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(list), destination.ID) || strings.Contains(string(list), foreign.ID) {
		t.Fatalf("space discovery: %s", list)
	}
	result, err := run("create-one", "tasks.create", map[string]any{"space": "family", "title": "Book dinner"})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := run("create-one", "tasks.create", map[string]any{"space": "family", "title": "Book dinner"})
	var originalValue, replayValue any
	_ = json.Unmarshal(result, &originalValue)
	_ = json.Unmarshal(replay, &replayValue)
	if err != nil || !reflect.DeepEqual(originalValue, replayValue) {
		t.Fatalf("replay: %s %v", replay, err)
	}
	if _, err = run("create-one", "tasks.create", map[string]any{"space": origin.ID, "title": "Book dinner"}); err == nil {
		t.Fatal("same call ID silently changed destination")
	}
	if _, err = run("query-foreign", "tasks.query", map[string]any{"space": foreign.ID}); err == nil {
		t.Fatal("foreign space accessible")
	}
	everywhere, err := run("query-all", "tasks.query", map[string]any{})
	if err != nil || !strings.Contains(string(everywhere), "Book dinner") || strings.Contains(string(everywhere), foreign.ID) {
		t.Fatalf("reads without a space cover every accessible Space: %s %v", everywhere, err)
	}
	if _, err = run("update-unrouted", "tasks.update", map[string]any{"id": "missing", "title": "Renamed"}); err == nil {
		t.Fatal("a change to an existing item must name its Space")
	}
}
