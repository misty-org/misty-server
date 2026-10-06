package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/modelruntime"
)

func TestRuntimeProviderCancelsExactlyOneRequest(t *testing.T) {
	started := make(chan struct{})
	releaseServer := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		close(started)
		<-releaseServer
	}))
	defer func() {
		close(releaseServer)
		server.Close()
	}()
	models := modelruntime.New(server.URL, []byte(strings.Repeat("s", 32)), server.Client())
	provider := NewRuntimeProvider(models, modelruntime.Instance(), "openai/test", ProviderVercelAI, "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := provider.NextContext(ctx, ModelRequest{SessionID: "s", UserID: "u", Messages: []Message{{Role: "user", Content: "wait"}}})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("model call did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("NextContext() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("model call was not canceled")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests = %d, want exactly 1 with no retry", got)
	}
}

func TestRuntimeProviderNeverFollowsRedirectsOrResends(t *testing.T) {
	var initialRequests, redirectedRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirectedRequests.Add(1)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		initialRequests.Add(1)
		http.Redirect(w, &http.Request{}, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	models := modelruntime.New(server.URL, []byte(strings.Repeat("s", 32)), server.Client())
	provider := NewRuntimeProvider(models, modelruntime.Instance(), "openai/test", "openai", "")
	var failure *modelruntime.Error
	if _, err := provider.Next(ModelRequest{SessionID: "s", UserID: "u", Messages: []Message{{Role: "user", Content: "once"}}}); !errors.As(err, &failure) || failure.Status != http.StatusTemporaryRedirect {
		t.Fatalf("Next() error = %v, want a terminal 307", err)
	}
	if initialRequests.Load() != 1 || redirectedRequests.Load() != 0 {
		t.Fatalf("initial requests=%d redirected requests=%d, want 1 and 0", initialRequests.Load(), redirectedRequests.Load())
	}
}

func TestRuntimeProviderAsksForTheAgentResponseSchema(t *testing.T) {
	runtime := modelruntime.TestingNewFake(t, func(call modelruntime.TestingCall) (int, any) {
		return http.StatusOK, map[string]any{
			"object": map[string]any{"text": "I will inspect the folder.", "tool_requests": []any{map[string]any{"name": "list_directory", "risk": "read", "arguments": map[string]any{"path": "Desktop"}}}, "file_plan": map[string]any{"summary": "", "completion_summary": "", "operations": []any{}, "warnings": []any{}}},
			"usage":  map[string]int{"inputTokens": 120, "cachedInputTokens": 20, "outputTokens": 40, "reasoningTokens": 10},
		}
	})
	provider := NewRuntimeProvider(runtime.Client, modelruntime.Instance(), "openai/gpt-test", ProviderVercelAI, "max")
	response, err := provider.Next(ModelRequest{
		SessionID: "s", UserID: "u", Mode: ModeAuto, ActiveRoot: "Desktop",
		Messages:     []Message{{Role: "user", Content: "Organize this"}},
		KnownPaths:   []string{"invoice.pdf"},
		Capabilities: ToolManifest{Tools: []ToolDefinition{{Name: ToolListDirectory, Risk: RiskRead}}},
	})
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if response.Text != "I will inspect the folder." || len(response.ToolRequests) != 1 || response.ToolRequests[0].Name != ToolListDirectory || response.ToolRequests[0].ID == "" {
		t.Fatalf("response = %#v", response)
	}
	if response.FilePlan != nil {
		t.Fatalf("empty wire file plan was not normalized: %#v", response.FilePlan)
	}
	if response.Usage.InputTokens != 120 || response.Usage.CachedInputTokens != 20 || response.Usage.OutputTokens != 40 || response.Usage.ReasoningTokens != 10 {
		t.Fatalf("usage = %#v", response.Usage)
	}
	calls := runtime.Calls()
	body := calls[0].Body
	if calls[0].Path != "/v1/models/text" || body["model"] != "openai/gpt-test" || body["route"].(map[string]any)["provider"] != "instance" || body["reasoning"] != "xhigh" {
		t.Fatalf("call = %s %v", calls[0].Path, body)
	}
	if body["schema"].(map[string]any)["name"] != "misty_agent_response" || body["maxOutputTokens"] != float64(MaxModelOutputTokens) {
		t.Fatalf("schema or output limit missing: %v", body)
	}
}

func TestGatewayAgentSchemaUsesPortableNonNullableFilePlan(t *testing.T) {
	schema := TestingAgentResponseJSONSchema()
	properties := schema["properties"].(map[string]any)
	filePlan := properties["file_plan"].(map[string]any)
	if filePlan["type"] != "object" {
		t.Fatalf("file_plan schema type = %#v", filePlan["type"])
	}
}
