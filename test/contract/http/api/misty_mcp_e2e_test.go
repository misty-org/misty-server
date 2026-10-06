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
