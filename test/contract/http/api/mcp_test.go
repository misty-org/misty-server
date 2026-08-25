package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	mcpintegration "github.com/kannachi323/misty/server/internal/integrations/mcp"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

type fakeMCPConnector struct {
	mu         sync.Mutex
	lastBearer string
	calls      int
}

func (fake *fakeMCPConnector) Test(context.Context, string, string) error { return nil }
func (fake *fakeMCPConnector) Discover(_ context.Context, _ string, bearer string) (mcpintegration.Discovery, error) {
	fake.mu.Lock()
	fake.lastBearer = bearer
	fake.mu.Unlock()
	return mcpintegration.Discovery{ProtocolVersion: "2026-07-28", ServerName: "test-mcp", ServerVersion: "1.0", Tools: []mcpintegration.Tool{
		{Name: "echo", Description: "Echo a message", InputSchema: json.RawMessage(`{"type":"object","required":["message"],"properties":{"message":{"type":"string","maxLength":200}}}`)},
		{Name: "unsafe-schema", Description: "Unsupported schema", InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string","pattern":".*"}}}`)},
	}}, nil
}
func (fake *fakeMCPConnector) CallTool(context.Context, string, string, string, json.RawMessage) (mcpintegration.CallResult, error) {
	fake.mu.Lock()
	fake.calls++
	fake.mu.Unlock()
	return mcpintegration.CallResult{Text: []string{"ok"}}, nil
}

func TestMCPConnectionDiscoveryAndManagedRuntimeContract(t *testing.T) {
	database := openPresenceTestDatabase(t)
	owner, err := database.CreateUser("MCP HTTP", uniqueTestEmail("mcp-http"), "password123")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := database.CreatePersonalAgent(t.Context(), owner.ID, db.PersonalAgent{Name: "MCP Agent", ModelMode: "pinned", ModelID: "google/gemini-2.5-flash-lite"})
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(t.Context(), owner.ID, "MCP Runtime")
	if err != nil {
		t.Fatal(err)
	}
	direct, err := database.DirectAgentConversation(t.Context(), owner.ID, space.ID, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("m", 32)))
	spaces, err := NewSpacesService(database, nil, key)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeMCPConnector{}
	spaces.TestingSetMCPConnectorClient(fake)
	router := chi.NewRouter()
	router.MethodFunc(http.MethodGet, "/mcp/connections", spaces.MCPConnections())
	router.MethodFunc(http.MethodPost, "/mcp/connections", spaces.MCPConnections())
	router.Post("/mcp/connections/{connectionID}/discover", spaces.DiscoverMCPConnection())
	router.Get("/mcp/connections/{connectionID}/tools", spaces.MCPConnectionTools())
	token := newConversationTestBearerToken(t, database, owner.ID)

	created := performConversationRequest(t, router, http.MethodPost, "/mcp/connections", token, map[string]any{"name": "Personal tools", "endpoint_url": "https://mcp.example.com/mcp", "bearer_token": "very-secret-token"})
	if created.Code != http.StatusCreated || strings.Contains(created.Body.String(), "very-secret-token") || strings.Contains(created.Body.String(), "cipher") {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	var createEnvelope struct {
		Connection struct {
			ID string `json:"id"`
		} `json:"connection"`
	}
	if json.Unmarshal(created.Body.Bytes(), &createEnvelope) != nil || createEnvelope.Connection.ID == "" {
		t.Fatalf("invalid create contract: %s", created.Body.String())
	}

	discovered := performConversationRequest(t, router, http.MethodPost, "/mcp/connections/"+createEnvelope.Connection.ID+"/discover", token, nil)
	if discovered.Code != http.StatusOK || strings.Contains(discovered.Body.String(), "very-secret-token") {
		t.Fatalf("discover=%d body=%s", discovered.Code, discovered.Body.String())
	}
	fake.mu.Lock()
	gotBearer := fake.lastBearer
	fake.mu.Unlock()
	if gotBearer != "very-secret-token" {
		t.Fatalf("provider received bearer %q", gotBearer)
	}
	var discoveryEnvelope struct {
		Tools []struct {
			RemoteName     string `json:"remote_name"`
			StableName     string `json:"stable_name"`
			SchemaStatus   string `json:"schema_status"`
			DisabledReason string `json:"disabled_reason"`
			DefaultRisk    string `json:"default_risk"`
			Approval       string `json:"approval"`
		} `json:"tools"`
	}
	if json.Unmarshal(discovered.Body.Bytes(), &discoveryEnvelope) != nil || len(discoveryEnvelope.Tools) != 2 {
		t.Fatalf("invalid discovery contract: %s", discovered.Body.String())
	}
	var echoName string
	for _, tool := range discoveryEnvelope.Tools {
		if tool.RemoteName == "echo" {
			echoName = tool.StableName
			if tool.SchemaStatus != "valid" || tool.DefaultRisk != "write" || tool.Approval != "interactive" {
				t.Fatalf("echo contract=%#v", tool)
			}
		}
		if tool.RemoteName == "unsafe-schema" && (tool.SchemaStatus != "unsupported" || tool.DisabledReason == "") {
			t.Fatalf("unsupported contract=%#v", tool)
		}
	}
	if !strings.HasPrefix(echoName, "mcp.") {
		t.Fatalf("stable name=%q", echoName)
	}

	if _, err := database.SetPersonalAgentMCPTools(t.Context(), owner.ID, agent.ID, []db.MCPAgentToolSelection{{
		ConnectionID: createEnvelope.Connection.ID,
		RemoteName:   "echo",
		Enabled:      true,
	}}); err != nil {
		t.Fatal(err)
	}
	run := &db.SpaceRun{ID: "mcp-run-" + agent.ID, RequestingMemberID: owner.ID, AgentID: agent.ID}
	request := serveragent.ToolRequest{
		ID: "call-1", Name: echoName, Arguments: json.RawMessage([]byte("{\"message\":\"once\"}")),
	}
	first, err := spaces.TestingExecuteMCPAgentTool(t.Context(), run, request, false, "contract")
	if err != nil || !strings.Contains(string(first), "\"provider\":\"mcp\"") {
		t.Fatalf("first MCP invocation=%s err=%v", first, err)
	}
	replayed, err := spaces.TestingExecuteMCPAgentTool(t.Context(), run, request, false, "contract")
	fake.mu.Lock()
	callCount := fake.calls
	fake.mu.Unlock()
	if err != nil || callCount != 1 || string(replayed) != `{}` {
		t.Fatalf("MCP retry result=%s remote calls=%d err=%v", replayed, callCount, err)
	}
	approvalRun, err := database.CreatePersonalAgentSpaceRun(t.Context(), owner.ID, space.ID, agent.ID, direct.ID, "direct", "direct", json.RawMessage(`{"prompt":"use MCP"}`), json.RawMessage(`{"allowed_tools":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	approvalRequest := serveragent.ToolRequest{ID: "approval-call", Name: echoName, Arguments: json.RawMessage(`{"message":"review me"}`)}
	if _, err := spaces.TestingExecuteMCPAgentTool(t.Context(), approvalRun, approvalRequest, true, "canonical_run"); !errors.Is(err, workflowv2.ErrAwaitingApproval) {
		t.Fatalf("unapproved canonical call error=%v, want awaiting approval", err)
	}
	fake.mu.Lock()
	callCount = fake.calls
	fake.mu.Unlock()
	if callCount != 1 {
		t.Fatalf("unapproved canonical call reached provider: %d calls", callCount)
	}
	if _, err := database.Conn.ExecContext(t.Context(), `UPDATE space_run_actions SET state='approved' WHERE run_id=$1 AND action_kind=$2`, approvalRun.ID, echoName); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Conn.ExecContext(t.Context(), `UPDATE space_runs SET state='running' WHERE id=$1`, approvalRun.ID); err != nil {
		t.Fatal(err)
	}
	approvedResult, err := spaces.TestingExecuteMCPAgentTool(t.Context(), approvalRun, approvalRequest, true, "canonical_run")
	if err != nil || !strings.Contains(string(approvedResult), `"provider":"mcp"`) {
		t.Fatalf("approved canonical result=%s err=%v", approvedResult, err)
	}
	fake.mu.Lock()
	callCount = fake.calls
	fake.mu.Unlock()
	if callCount != 2 {
		t.Fatalf("approved canonical call count=%d, want 2 total", callCount)
	}
}

func TestMCPCompanionCallsAreAlwaysDangerous(t *testing.T) {
	if impact := TestingCompanionToolImpact("mcp.0123456789ab.echo"); impact != "dangerous" {
		t.Fatalf("MCP companion impact=%q, want dangerous", impact)
	}
	if !TestingCompanionToolNeedsApproval("full", "dangerous") {
		t.Fatal("MCP companion call bypassed creator approval in full mode")
	}
}
