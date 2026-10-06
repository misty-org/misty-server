package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"testing"
)

func TestRegistryRejectsRemoteSchemasAtRegistration(t *testing.T) {
	_, err := New(Registration{Descriptor: Descriptor{Name: "remote.read", Version: 1, Description: "Remote", Risk: serveragent.RiskRead, Approval: ApprovalNone, Locality: LocalityProvider, InputSchema: json.RawMessage(`{"$ref":"https://outside.invalid/schema"}`)}, Handler: func(context.Context, Invocation, serveragent.ToolRequest) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}})
	if !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("accepted remote schema: %v", err)
	}
}
