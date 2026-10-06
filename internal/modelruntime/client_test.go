package modelruntime

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/kannachi323/misty/server/internal/aimodels"
)

func TestUnconfiguredRuntimeMakesNoModelCalls(t *testing.T) {
	t.Setenv("MISTY_ENVIRONMENT", "")
	t.Setenv("MISTY_AGENT_RUNTIME_URL", "")
	t.Setenv("MISTY_AGENT_RUNTIME_CONTROL_SECRET", "")
	client, err := FromEnv()
	if err != nil || client.Enabled() {
		t.Fatalf("FromEnv() = %v, %v", client, err)
	}
	if _, err := client.Text(t.Context(), TextRequest{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Text() error = %v", err)
	}
	var missing *Client
	if missing.Enabled() {
		t.Fatal("nil client enabled")
	}
	t.Setenv("MISTY_ENVIRONMENT", "production")
	if _, err := FromEnv(); err == nil {
		t.Fatal("production started without a model runtime")
	}
}

func TestFromEnvRequiresHTTPSAndAStrongSecret(t *testing.T) {
	secret := "c2VjcmV0LXNlY3JldC1zZWNyZXQtc2VjcmV0LXNlY3JldA=="
	for _, tc := range []struct{ url, secret string }{
		{"http://runtime.example", secret},
		{"https://runtime.example", "c2hvcnQ="},
		{"runtime", secret},
	} {
		t.Setenv("MISTY_AGENT_RUNTIME_URL", tc.url)
		t.Setenv("MISTY_AGENT_RUNTIME_CONTROL_SECRET", tc.secret)
		if _, err := FromEnv(); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	t.Setenv("MISTY_AGENT_RUNTIME_URL", "http://agent-runtime:3000")
	if client, err := FromEnv(); err != nil || !client.Enabled() {
		t.Fatalf("local runtime rejected: %v", err)
	}
}

func TestCallsAreSignedAndFailuresCarryOnlyStatus(t *testing.T) {
	fake := TestingNewFake(t, func(call TestingCall) (int, any) {
		if call.Path == "/v1/models/embed" {
			return http.StatusBadGateway, map[string]any{"code": "model_call_failed", "message": "The model provider could not complete the request.", "upstream_status": 429}
		}
		return http.StatusOK, map[string]any{"text": "ok", "usage": map[string]int{"inputTokens": 4, "outputTokens": 2}}
	})
	result, err := fake.Client.Text(t.Context(), TextRequest{Route: Instance(), Model: "openai/gpt-test", Messages: []Message{{Role: "user", Content: []Part{Text("hi")}}}, MaxOutputTokens: 10})
	if err != nil || result.Text != "ok" || result.Usage.InputTokens != 4 {
		t.Fatalf("Text() = %+v, %v", result, err)
	}
	_, err = fake.Client.Embed(t.Context(), EmbedRequest{Route: Instance(), Model: "google/x", Values: []string{"a"}})
	var failure *Error
	if !errors.As(err, &failure) || failure.UpstreamStatus != 429 || !failure.Temporary() || strings.Contains(err.Error(), "{") {
		t.Fatalf("Embed() error = %#v", err)
	}
	unsigned := New(fake.Server.URL, []byte(strings.Repeat("x", 32)), nil)
	if _, err := unsigned.Text(t.Context(), TextRequest{}); !errors.As(err, &failure) || failure.Status != http.StatusUnauthorized {
		t.Fatalf("wrong secret accepted: %v", err)
	}
}

func TestRoutesAreAlwaysInstanceAndCarryOnlyReasoning(t *testing.T) {
	if route := For(nil); route != Instance() {
		t.Fatalf("default route = %+v", route)
	}
	route := For(&aimodels.Resolved{Model: "openai/x", Reasoning: "low"})
	if route.Provider != "instance" || route.Reasoning != "low" {
		t.Fatalf("account route = %+v", route)
	}
}
