package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	cap "github.com/kannachi323/misty/server/internal/capabilities"
)

func TestProviderRegistryKeepsTargetsDistinctAndEnforcesPolicyAndSchemas(t *testing.T) {
	definition := cap.Definition{Name: "habits.record", Version: 1, Description: "Record a habit", RequiredScopes: []string{"habits.record"}, InputSchema: json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer","minimum":1}},"required":["count"],"additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"recorded":{"const":true}},"required":["recorded"],"additionalProperties":false}`), Effects: cap.Effects{Kind: "write", Approval: "scoped", Retry: "idempotent"}}
	provider := cap.Provider{ID: "example.habits/backend", Version: 1, Label: "Habits", Route: cap.Route{Kind: "backend", ConnectionID: "10000000-0000-4000-8000-000000000001"}, Capabilities: []cap.Definition{definition}}
	target := cap.Target{ID: "20000000-0000-4000-8000-000000000001", Revision: 1, AppID: "example.habits", ProviderID: provider.ID, ProviderVersion: 1, Label: "Habits account", Binding: json.RawMessage(`{"kind":"backend","connectionId":"10000000-0000-4000-8000-000000000001"}`)}
	calls := 0
	reply := json.RawMessage(`{"recorded":true}`)
	handler := func(context.Context, Invocation, serveragent.ToolRequest) (json.RawMessage, error) {
		calls++
		return reply, nil
	}
	first, err := ProviderRegistration(provider, target, definition, handler)
	if err != nil {
		t.Fatal(err)
	}
	target.ID = "20000000-0000-4000-8000-000000000002"
	second, err := ProviderRegistration(provider, target, definition, handler)
	if err != nil {
		t.Fatal(err)
	}
	if first.Descriptor.Name == second.Descriptor.Name {
		t.Fatal("different targets collided")
	}
	registry, err := New(first, second)
	if err != nil {
		t.Fatal(err)
	}
	first.Descriptor.ProviderBinding.RequiredScopes[0] = "admin.write"
	if registry.Descriptors()[0].ProviderBinding.RequiredScopes[0] != "habits.record" {
		t.Fatal("registration caller retained a mutable scope pointer")
	}
	invocation := Invocation{UserID: "owner"}
	call := serveragent.ToolRequest{ID: "effect", Name: first.Descriptor.Name, Arguments: json.RawMessage(`{"count":1}`)}
	if _, err := registry.Execute(context.Background(), invocation, call, nil); !errors.Is(err, ErrApprovalRequired) || calls != 0 {
		t.Fatalf("scoped manifest bypassed approval: %v", err)
	}
	invocation.ApprovedTools = map[string]bool{call.Name: true}
	call.Arguments = json.RawMessage(`{"count":0}`)
	if _, err := registry.Execute(context.Background(), invocation, call, nil); !errors.Is(err, ErrArgumentsInvalid) || calls != 0 {
		t.Fatalf("schema constraint not enforced: %v", err)
	}
	call.Arguments = json.RawMessage(`{"count":1}`)
	if _, err := registry.Execute(context.Background(), invocation, call, func(context.Context, Invocation, Descriptor) (bool, error) { return false, nil }); !errors.Is(err, ErrCapabilityDenied) || calls != 0 {
		t.Fatalf("authorizer bypassed: %v", err)
	}
	if _, err := registry.Execute(context.Background(), invocation, call, nil); err != nil || calls != 1 {
		t.Fatalf("approved call: %v", err)
	}
	reply = json.RawMessage(`{"recorded":false}`)
	if _, err := registry.Execute(context.Background(), invocation, call, nil); !errors.Is(err, ErrResultInvalid) {
		t.Fatalf("false result confirmed: %v", err)
	}
	snapshot := registry.Descriptors()
	snapshot[0].ProviderBinding.RequiredScopes[0] = "admin.write"
	if registry.Descriptors()[0].ProviderBinding.RequiredScopes[0] != "habits.record" {
		t.Fatal("descriptor leaked mutable grant metadata")
	}
}
