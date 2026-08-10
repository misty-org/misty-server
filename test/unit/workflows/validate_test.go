package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/workflows"
)

func node(id, kind string) Node {
	return Node{ID: id, Kind: kind, KindVersion: 1, Label: id, Config: json.RawMessage(`{}`), OutputSchema: JSONSchema{"type": "object"}, Retry: DefaultRetryPolicy(), Errors: ErrorPolicy{Mode: "fail"}}
}

func definition(nodes []Node, edges []Edge, capabilities ...CapabilityRequirement) Definition {
	return Definition{FormatVersion: FormatVersion, Inputs: JSONSchema{"type": "object"}, Outputs: JSONSchema{"type": "object"}, Capabilities: capabilities, Nodes: nodes, Edges: edges, Dependencies: []WorkflowDependency{}}
}

func TestValidateTypedGraphAndCapabilities(t *testing.T) {
	value := definition(
		[]Node{node("start", "manual_trigger"), node("read", "read_content"), node("reason", "agent_task")},
		[]Edge{{ID: "a", Source: "start", SourcePort: "event", Target: "read", TargetPort: "content"}, {ID: "b", Source: "read", SourcePort: "page", Target: "reason", TargetPort: "input"}},
		CapabilityRequirement{Capability: "triggers.read", Risk: RiskRead},
		CapabilityRequirement{Capability: "content.read", Risk: RiskRead},
		CapabilityRequirement{Capability: "agent.reason", Risk: RiskRead},
	)
	if err := Validate(value, CoreRegistry(), nil); err != nil {
		t.Fatalf("valid graph rejected: %v", err)
	}
	value.Edges = append(value.Edges, Edge{ID: "cycle", Source: "reason", SourcePort: "output", Target: "read", TargetPort: "again"})
	if err := Validate(value, CoreRegistry(), nil); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("cycle error = %v", err)
	}
}

func TestValidateRejectsMissingProviderAndCapabilityExpansion(t *testing.T) {
	value := definition([]Node{node("unknown", "not_registered")}, nil)
	if err := Validate(value, CoreRegistry(), nil); !errors.Is(err, ErrProviderMissing) {
		t.Fatalf("missing provider error = %v", err)
	}
	value = definition([]Node{node("write", "create_document")}, nil, CapabilityRequirement{Capability: "files.write", Risk: RiskRead})
	if err := Validate(value, CoreRegistry(), nil); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("capability error = %v", err)
	}
}

func TestRegistryRequiresSafeMutationRetryContract(t *testing.T) {
	registry := NewRegistry()
	err := registry.Register(NodeDescriptor{Kind: "unsafe", Version: 1, Capability: "files.write", Risk: RiskWrite, Execute: func(context.Context, Invocation) (json.RawMessage, error) { return nil, nil }})
	if !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("unsafe mutation registration = %v", err)
	}
}

func TestValidateRequiresCanonicalRetryPolicy(t *testing.T) {
	value := definition([]Node{node("start", "manual_trigger")}, nil, CapabilityRequirement{Capability: "triggers.read", Risk: RiskRead})
	value.Nodes[0].Retry = RetryPolicy{MaxAttempts: 2, CooldownSeconds: 1}
	if err := Validate(value, CoreRegistry(), nil); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("retry policy error = %v", err)
	}
}
