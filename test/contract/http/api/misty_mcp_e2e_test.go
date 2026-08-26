package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (transport bearerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+transport.token)
	return transport.base.RoundTrip(clone)
}

func TestManagedMistyMCPNegotiatesWithOfficialGoSDK(t *testing.T) {
	database := openPresenceTestDatabase(t)
	email := uniqueTestEmail("misty-mcp-e2e")
	username := "mistymcp_" + strings.ReplaceAll(uuid.NewString()[:12], "-", "")
	owner, err := database.CreateUserWithUsername("Misty MCP", username, email, "password123")
	if err != nil {
		t.Fatal(err)
	}
	space, err := database.CreateSpace(t.Context(), owner.ID, "Misty MCP Contract")
	if err != nil {
		t.Fatal(err)
	}
	connection, err := database.CreateMCPRemoteConnection(t.Context(), db.MCPRemoteConnection{
		OwnerUserID: owner.ID, Name: "Contract remote", EndpointURL: "https://mcp.example.test",
		BearerCiphertext: []byte{}, BearerNonce: []byte{}, KeyVersion: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	remoteName := "mcp.contract.remote_echo"
	if _, err := database.SaveMCPDiscovery(t.Context(), owner.ID, db.MCPDiscoverySnapshot{
		ConnectionID: connection.ID, ProtocolVersion: "2026-07-28", ServerName: "contract-remote",
		ServerVersion: "1.0.0", CatalogFingerprint: strings.Repeat("c", 64), ToolCount: 1, Status: "complete",
	}, []db.MCPRemoteTool{{
		ConnectionID: connection.ID, RemoteName: "remote_echo", StableName: remoteName,
		Description:       "Echo through a connected MCP server.",
		InputSchema:       json.RawMessage(`{"type":"object","required":["message"],"properties":{"message":{"type":"string"}}}`),
		SchemaFingerprint: strings.Repeat("f", 64), SchemaStatus: "valid",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetMCPConnectionHealth(t.Context(), owner.ID, connection.ID, "active", "", true); err != nil {
		t.Fatal(err)
	}
	misty, err := database.EnsureManagedMistyAgent(t.Context(), owner.ID, serveragent.InitialSelectedModelID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := database.CreateCreatorAgentRun(t.Context(), owner.ID, space.ID, misty.ID, db.CreatorAgentRunInput{
		Instruction: "Inspect the available Misty tools.",
	})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := database.ClaimPersonalAgentTaskRunJobs(t.Context(), "mcp-contract-worker", 1, time.Minute)
	if err != nil || len(jobs) != 1 || jobs[0].Run.ID != run.ID {
		t.Fatalf("claimed jobs = %#v, err = %v", jobs, err)
	}
	runtimeRunID := "workflow-mcp-contract-" + uuid.NewString()
	if _, err := database.ActivatePersonalAgentTaskRuntime(t.Context(), run.ID, "vercel-workflow", runtimeRunID); err != nil {
		t.Fatal(err)
	}

	secret := []byte(strings.Repeat("s", 32))
	t.Setenv("MISTY_AGENT_RUNTIME_URL", "https://runtime.test")
	t.Setenv("MISTY_AGENT_RUNTIME_INTERNAL_API_URL", "https://api.test")
	t.Setenv("MISTY_AGENT_RUNTIME_CONTROL_SECRET", base64.StdEncoding.EncodeToString(secret))
	runtimeConfig, err := AgentRuntimeConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	spaces, err := NewSpacesService(database, nil, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	spaces.SetAgentRuntime(runtimeConfig)

	now := time.Now().UTC()
	token, err := TestingSignMCPAccessToken(secret, owner.ID, run.ID, runtimeRunID, "mcp-contract-token", "", now, now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(spaces.MistyMCP())
	t.Cleanup(server.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "misty-contract-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:             server.URL,
		HTTPClient:           &http.Client{Transport: bearerRoundTripper{token: token, base: http.DefaultTransport}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var contextTool, drawingCreateTool, drawingApplyTool, remoteTool *mcp.Tool
	for _, tool := range result.Tools {
		if tool.Name == "agents.list" || tool.Name == "agents.status" {
			t.Fatalf("managed Misty exposed the retired Agent roster tool %q", tool.Name)
		}
		if tool.Name == "context.get" {
			contextTool = tool
		}
		if tool.Name == "drawings.create" {
			drawingCreateTool = tool
		}
		if tool.Name == "drawings.apply" {
			drawingApplyTool = tool
		}
		if tool.Name == remoteName {
			remoteTool = tool
		}
	}
	if contextTool == nil {
		t.Fatalf("context.get missing from %d run-scoped tools", len(result.Tools))
	}
	if contextTool.InputSchema == nil || contextTool.OutputSchema == nil || contextTool.Annotations == nil || !contextTool.Annotations.ReadOnlyHint {
		t.Fatalf("context.get is missing typed schemas or read-only metadata: %#v", contextTool)
	}
	if contextTool.Meta["misty/risk"] != "read" || contextTool.Meta["misty/version"] == nil {
		t.Fatalf("context.get is missing Misty metadata: %#v", contextTool.Meta)
	}
	if drawingCreateTool == nil || drawingApplyTool == nil {
		t.Fatalf("drawing write tools are missing from the run-scoped MCP catalog")
	}
	if drawingCreateTool.Meta["misty/risk"] != "write" || drawingApplyTool.Meta["misty/risk"] != "write" {
		t.Fatalf("drawing tools are missing write-risk metadata")
	}
	if remoteTool == nil || remoteTool.InputSchema == nil || remoteTool.OutputSchema == nil {
		t.Fatalf("connected remote MCP tool is not advertised with typed schemas: %#v", remoteTool)
	}
	if remoteTool.Meta["misty/approval"] != "interactive" || remoteTool.Meta["misty/locality"] != "provider" {
		t.Fatalf("remote MCP tool is missing approval/provider metadata: %#v", remoteTool.Meta)
	}
}

func TestAIInvocationMCPAdvertisesWeatherThroughOfficialGoSDK(t *testing.T) {
	database := openPresenceTestDatabase(t)
	owner, err := database.CreateUserWithUsername(
		"AI MCP",
		"aimcp_"+strings.ReplaceAll(uuid.NewString()[:12], "-", ""),
		uniqueTestEmail("ai-mcp-e2e"),
		"password123",
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	invocationID := "invocation_" + uuid.NewString()
	requestPayload := json.RawMessage(`{
		"mode":"quick",
		"surface_id":"settings",
		"trigger":"message",
		"prompt":"What is the weather in Arcadia, CA?",
		"context":[],
		"idempotency_key":"mcp-weather-contract",
		"timezone":"America/Los_Angeles"
	}`)
	if _, created, createErr := database.CreateAIInvocationRecord(t.Context(), db.AIInvocationRecord{
		ID: invocationID, UserID: owner.ID, SurfaceID: "settings", Mode: "quick",
		Trigger: "message", State: "queued", IdempotencyKey: "mcp-weather-contract",
		RequestPayload: requestPayload, ExpiresAt: now.Add(time.Hour),
	}); createErr != nil || !created {
		t.Fatalf("create AI invocation: created=%v err=%v", created, createErr)
	}
	runtimeRunID := "workflow-ai-mcp-contract-" + uuid.NewString()
	if _, err := database.ActivateAIInvocationRuntime(t.Context(), invocationID, "vercel-workflow", runtimeRunID); err != nil {
		t.Fatal(err)
	}

	secret := []byte(strings.Repeat("s", 32))
	t.Setenv("MISTY_AGENT_RUNTIME_URL", "https://runtime.test")
	t.Setenv("MISTY_AGENT_RUNTIME_INTERNAL_API_URL", "https://api.test")
	t.Setenv("MISTY_AGENT_RUNTIME_CONTROL_SECRET", base64.StdEncoding.EncodeToString(secret))
	runtimeConfig, err := AgentRuntimeConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	spaces, err := NewSpacesService(database, nil, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	spaces.SetAgentRuntime(runtimeConfig)
	token, err := TestingSignMCPAccessToken(secret, owner.ID, invocationID, runtimeRunID, "ai-mcp-contract-token", "", now, now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(spaces.MistyMCP())
	t.Cleanup(server.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "misty-ai-contract-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:             server.URL,
		HTTPClient:           &http.Client{Transport: bearerRoundTripper{token: token, base: http.DefaultTransport}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, availableTool := range result.Tools {
		if availableTool.Name != "weather.current" {
			continue
		}
		if availableTool.InputSchema == nil || availableTool.OutputSchema == nil || availableTool.Annotations == nil || !availableTool.Annotations.ReadOnlyHint {
			t.Fatalf("weather.current is missing typed schemas or read-only metadata: %#v", availableTool)
		}
		return
	}
	t.Fatalf("weather.current missing from %d AI invocation tools", len(result.Tools))
}
