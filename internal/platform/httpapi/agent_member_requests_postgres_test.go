package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/internal/workflow"
	"github.com/kannachi323/misty/server/test/testkit"
)

// memberBillingMeter records which account each model call is charged to.
// The requester's own turns are billed by its own run, as before.
type memberBillingMeter struct {
	reserved, completed []string
	commands            []string
}

func (m *memberBillingMeter) Reserve(userID, _, _, _, _ string, _, _ int64) (*agent.UsageReservation, error) {
	m.reserved = append(m.reserved, userID)
	return &agent.UsageReservation{}, nil
}
func (m *memberBillingMeter) ReserveMeasured(userID, _, _, _ string, _ map[string]int64, command string) (*agent.UsageReservation, error) {
	m.reserved, m.commands = append(m.reserved, userID), append(m.commands, command)
	return &agent.UsageReservation{}, nil
}
func (m *memberBillingMeter) CompleteRuntime(_ context.Context, account, _ string, _ agent.ModelUsage) error {
	m.completed = append(m.completed, account)
	return nil
}
func (*memberBillingMeter) Settle(*agent.UsageReservation, string, string, string, string, agent.ModelUsage) (agent.UsageSettlement, error) {
	return agent.UsageSettlement{}, nil
}
func (*memberBillingMeter) Release(*agent.UsageReservation) error { return nil }
func (*memberBillingMeter) Refund(*agent.UsageReservation, string, string) (agent.UsageSettlement, error) {
	return agent.UsageSettlement{}, nil
}

func TestAgentMemberRequestToolsAndBillingPostgres(t *testing.T) {
	if os.Getenv("TEST_DB_NAME") != "misty_agent_members_test" {
		t.Skip("requires isolated agent members test database")
	}
	database := testkit.OpenDatabase(t)
	ctx := t.Context()
	exec := func(query string, args ...any) {
		t.Helper()
		if err := database.SpaceTx(ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(ctx, query, args...); return err }); err != nil {
			t.Fatal(err)
		}
	}
	alice, _ := database.CreateUser("alice-members@example.test", "password123")
	bob, _ := database.CreateUser("bob-members@example.test", "password123")
	space, err := database.TestingCreateSpace(ctx, alice.ID, "Launch team")
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO space_members(space_id,user_id,role) VALUES($1,$2,'member')`, space.ID, bob.ID)
	bobAgent, _ := database.SavePersonalAgent(ctx, bob.ID, "", db.AgentProfileInput{Name: "Researcher", Enabled: true})
	aliceAgent, _ := database.SavePersonalAgent(ctx, alice.ID, "", db.AgentProfileInput{Name: "Planner", Enabled: true})
	aliceRun, err := database.CreateCreatorAgentRun(ctx, alice.ID, "", aliceAgent.ID, db.CreatorAgentRunInput{Instruction: "Plan the launch"})
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE space_runs SET state='running' WHERE id=$1`, aliceRun.ID)
	if _, err := database.AddSpaceAgent(ctx, bob.ID, space.ID, bobAgent.ID); err != nil {
		t.Fatal(err)
	}
	meter := &memberBillingMeter{}
	service := &SpacesService{database: database, usageMeter: meter}

	// The requester's own run offers the member tools.
	parent, _ := database.SpaceRun(ctx, alice.ID, aliceRun.ID)
	parentToolbox, _, _, err := service.resolvePersonalAgentRuntimeToolbox(ctx, parent)
	if err != nil || !hasToolboxTool(parentToolbox, agentsRequestTool) || !hasToolboxTool(parentToolbox, agentsDirectoryTool) {
		t.Fatalf("requester catalog lacks member tools: %v", err)
	}
	invocation := agenttools.Invocation{UserID: alice.ID, AgentID: aliceAgent.ID, RunID: aliceRun.ID}
	directory, err := executeAgentsDirectory(ctx, database, invocation, agent.ToolRequest{Arguments: json.RawMessage(`{}`)})
	if err != nil || !strings.Contains(string(directory), bobAgent.ID) {
		t.Fatalf("directory = %s, %v", directory, err)
	}
	raw, err := executeAgentsRequest(ctx, database, invocation, agent.ToolRequest{ID: "call-1", Arguments: json.RawMessage(`{"agent":"Researcher","message":"Find the launch date","wait_seconds":0}`)})
	if err != nil {
		t.Fatal(err)
	}
	var sent struct {
		RequestID string `json:"request_id"`
		State     string `json:"state"`
	}
	// The work starts at once; nobody approves it.
	if json.Unmarshal(raw, &sent) != nil || sent.State != "submitted" {
		t.Fatalf("request = %s", raw)
	}
	request, err := database.AgentMemberRequestForUser(ctx, alice.ID, sent.RequestID)
	if err != nil {
		t.Fatal(err)
	}

	// The delegated run gets Space tools and reads Bob's agent's memory, and
	// Alice pays for the work she asked for.
	exec(`UPDATE space_runs SET state='running',runtime_run_id='runtime-1' WHERE id=$1`, request.ChildRunID)
	child, _ := database.SpaceRun(ctx, bob.ID, request.ChildRunID)
	// The run context freezes Bob's own model routes when the work starts.
	if _, err := database.FreezeAIModelRoutes(ctx, bob.ID, child.ID, defaultAIRoutes("", ""), nil); err != nil {
		t.Fatal(err)
	}
	childToolbox, childInvocation, authorize, err := service.resolvePersonalAgentRuntimeToolbox(ctx, child)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{agentsRequestTool, toolboxMemoryRemember, toolboxMemoryForget, "agent.configure", appsExecuteTool, "space.list", toolboxAgentsDelegate} {
		if hasToolboxTool(childToolbox, forbidden) {
			t.Fatalf("delegated run offers %s", forbidden)
		}
	}
	if !hasToolboxTool(childToolbox, "note.search") || !hasToolboxTool(childToolbox, "memory.pref.list") || childInvocation.UserID != bob.ID || childInvocation.SpaceID != space.ID {
		t.Fatalf("delegated invocation = %#v", childInvocation)
	}
	if _, err := service.meterPersonalAgentRuntimeModel(ctx, child, "model:1", workflow.StepRunning, json.RawMessage(`{"input_bytes":64}`)); err != nil {
		t.Fatal(err)
	}
	if err := service.settlePersonalAgentRuntimeUsage(ctx, child, "success", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(meter.reserved, []string{alice.ID}) || !slices.Equal(meter.completed, []string{alice.ID}) || !slices.Equal(meter.commands, []string{"agent-runtime:" + child.ID}) {
		t.Fatalf("billing reserved=%v completed=%v commands=%v; want the member who asked", meter.reserved, meter.completed, meter.commands)
	}

	// Taking the agent out of the Space stops the work at its next tool call.
	if err := database.RemoveSpaceAgent(ctx, bob.ID, space.ID, bobAgent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := childToolbox.Execute(ctx, childInvocation, agent.ToolRequest{ID: "c1", Name: "note.search", Arguments: json.RawMessage(`{"query":"launch"}`)}, authorize); err == nil {
		t.Fatal("agent kept working after it left the Space")
	}

	// The requester reads the finished reply.
	if _, err := database.FinishSpaceRun(ctx, child.ID, "completed", MustAPIRawJSON(map[string]any{"text": "Launch is Friday."}), ""); err != nil {
		t.Fatal(err)
	}
	status, err := executeAgentsRequestStatus(ctx, database, invocation, agent.ToolRequest{Arguments: json.RawMessage(`{"request_id":"` + sent.RequestID + `","wait_seconds":0}`)})
	if err != nil || !strings.Contains(string(status), `"state":"completed"`) || !strings.Contains(string(status), "Launch is Friday.") {
		t.Fatalf("status = %s, %v", status, err)
	}
}

func hasToolboxTool(toolbox *agenttools.Registry, name string) bool {
	for _, descriptor := range toolbox.Descriptors() {
		if descriptor.Name == name {
			return true
		}
	}
	return false
}
