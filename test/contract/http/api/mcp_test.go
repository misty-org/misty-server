package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	mcpintegration "github.com/kannachi323/misty/server/internal/integrations/mcp"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
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
	agent, err := database.EnsureAskIdentity(t.Context(), owner.ID, "google/gemini-2.5-flash-lite")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(t.Context(), owner.ID, "MCP Runtime")
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
	if err != nil || callCount != 1 || !strings.Contains(string(replayed), `"provider":"mcp"`) || !strings.Contains(string(replayed), `"ok"`) {
		t.Fatalf("MCP retry result=%s remote calls=%d err=%v", replayed, callCount, err)
	}
	canonicalRun, err := database.CreateCreatorAgentRun(t.Context(), owner.ID, space.ID, agent.ID, db.CreatorAgentRunInput{Instruction: "use MCP"})
	if err != nil {
		t.Fatal(err)
	}
	if jobs, err := database.ClaimPersonalAgentTaskRunJobs(t.Context(), "mcp-test", 1, time.Minute); err != nil || len(jobs) != 1 {
		t.Fatalf("claim MCP run: %v %v", jobs, err)
	}
	if _, err := database.ActivatePersonalAgentTaskRuntime(t.Context(), canonicalRun.ID, "test", "mcp-test"); err != nil {
		t.Fatal(err)
	}
	canonicalRequest := serveragent.ToolRequest{ID: "canonical-call", Name: echoName, Arguments: json.RawMessage(`{"message":"execute once"}`)}
	// Creator-enabled tools execute directly, including the legacy approval flag.
	// Retrying the same canonical action must still avoid a second provider call.
	for attempt := 0; attempt < 2; attempt++ {
		result, err := spaces.TestingExecuteMCPAgentTool(t.Context(), canonicalRun, canonicalRequest, true, "canonical_run")
		if err != nil || !strings.Contains(string(result), `"provider":"mcp"`) {
			t.Fatalf("canonical attempt %d result=%s err=%v", attempt, result, err)
		}
		fake.mu.Lock()
		callCount = fake.calls
		fake.mu.Unlock()
		if callCount != 2 {
			t.Fatalf("canonical attempt %d call count=%d, want 2 total", attempt, callCount)
		}
	}
}

func TestMCPCompanionCallsAreAlwaysDangerous(t *testing.T) {
	if impact := TestingCompanionToolImpact("mcp.0123456789ab.echo"); impact != "dangerous" {
		t.Fatalf("MCP companion impact=%q, want dangerous", impact)
	}
	for _, mode := range []string{"ask", "auto", "full"} {
		if TestingCompanionToolNeedsApproval(mode, "dangerous") {
			t.Fatalf("creator-enabled MCP tool unexpectedly requires per-action approval in %s mode", mode)
		}
	}
}
