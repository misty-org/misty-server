package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/kannachi323/misty/server/internal/agent"
	"testing"
)

func TestRegistryRejectsRemoteSchemasAtRegistration(t *testing.T) {
	_, err := New(Registration{Descriptor: Descriptor{Name: "remote.read", Version: 1, Description: "Remote", Risk: agent.RiskRead, Locality: LocalityProvider, Layer: LayerPublic, InputSchema: json.RawMessage(`{"$ref":"https://outside.invalid/schema"}`)}, Handler: func(context.Context, Invocation, agent.ToolRequest) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}})
	if !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("accepted remote schema: %v", err)
	}
}
