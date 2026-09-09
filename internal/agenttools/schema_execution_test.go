package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/capabilities"
	"testing"
)

func TestRegistryEnforcesSDKInteractionSchemaBeforeEffects(t *testing.T) {
	calls := 0
	registry, err := New(Registration{Descriptor: Descriptor{Name: "browser.interact", Version: 1, Description: "Interact", Risk: serveragent.RiskWrite, AuditEvent: "browser.interacted", Approval: ApprovalNone, Locality: LocalityDevice, InputSchema: capabilities.BrowserInteractionSchema(), OutputSchema: json.RawMessage(`{"type":"object","properties":{"attempted":{"const":true}},"required":["attempted"],"additionalProperties":false}`)}, Handler: func(context.Context, Invocation, serveragent.ToolRequest) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{"attempted":true}`), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		`{"kind":"scroll","x":0,"y":4001}`,
		`{"kind":"key","elementRef":"e","key":"Meta"}`,
		`{"kind":"fill","elementRef":"e"}`,
		`{"kind":"select","elementRef":"e","values":[]}`,
		`{"kind":"scroll","x":0,"y":20,"script":"alert(1)"}`,
	} {
		if _, err := registry.Execute(t.Context(), Invocation{}, serveragent.ToolRequest{Name: "browser.interact", Arguments: json.RawMessage(input)}, nil); !errors.Is(err, ErrArgumentsInvalid) {
			t.Fatalf("accepted invalid action %s: %v", input, err)
		}
	}
	if calls != 0 {
		t.Fatal("invalid actions reached execution")
	}
	if _, err := registry.Execute(t.Context(), Invocation{}, serveragent.ToolRequest{Name: "browser.interact", Arguments: json.RawMessage(`{"kind":"scroll","x":0,"y":100}`)}, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("valid action was not executed")
	}
}

func TestRegistryRejectsRemoteSchemasAtRegistration(t *testing.T) {
	_, err := New(Registration{Descriptor: Descriptor{Name: "remote.read", Version: 1, Description: "Remote", Risk: serveragent.RiskRead, Approval: ApprovalNone, Locality: LocalityProvider, InputSchema: json.RawMessage(`{"$ref":"https://outside.invalid/schema"}`)}, Handler: func(context.Context, Invocation, serveragent.ToolRequest) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}})
	if !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("accepted remote schema: %v", err)
	}
}
