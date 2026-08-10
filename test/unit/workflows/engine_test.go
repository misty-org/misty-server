package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	. "github.com/kannachi323/misty/server/internal/workflows"
)

func TestEngineRetriesThreeTimesWithStableIdempotencyAndPartialCompletion(t *testing.T) {
	registry := NewRegistry()
	attempts, keys := 0, []string{}
	_ = registry.Register(NodeDescriptor{Kind: "flaky", Version: 1, Capability: "test.read", Risk: RiskRead, Location: LocationCloud, Idempotent: true, InputSchema: JSONSchema{"type": "object"}, OutputSchema: JSONSchema{"type": "object"}, HealthCheck: func(context.Context) error { return nil }, Cancel: func(context.Context, Invocation) error { return nil }, Execute: func(_ context.Context, invocation Invocation) (json.RawMessage, error) {
		attempts++
		keys = append(keys, invocation.IdempotencyKey)
		return nil, errors.New("provider unavailable")
	}})
	definition := Definition{FormatVersion: 2, Inputs: JSONSchema{"type": "object"}, Outputs: JSONSchema{"type": "object"}, Capabilities: []CapabilityRequirement{{Capability: "test.read", Risk: RiskRead}}, Nodes: []Node{{ID: "item", Kind: "flaky", KindVersion: 1, Config: json.RawMessage(`{}`), OutputSchema: JSONSchema{"type": "object"}, Retry: DefaultRetryPolicy(), Errors: ErrorPolicy{Mode: "collect"}}}, Dependencies: []WorkflowDependency{}}
	cooldowns := 0
	result, err := (Engine{Registry: registry, Cooldown: func(context.Context, StepEvent, int) error { cooldowns++; return nil }}).Execute(context.Background(), definition, ExecutionRequest{RunID: "run-1", Input: json.RawMessage(`{}`)})
	if err != nil || result.State != RunCompletedWithErrors || attempts != 3 || cooldowns != 2 {
		t.Fatalf("result=%#v err=%v attempts=%d cooldowns=%d", result, err, attempts, cooldowns)
	}
	if keys[0] == "" || keys[0] != keys[1] || keys[1] != keys[2] {
		t.Fatalf("idempotency keys changed across retries: %#v", keys)
	}
}

func TestEngineValidatesOutputsBeforeContinuing(t *testing.T) {
	registry := NewRegistry()
	_ = registry.Register(NodeDescriptor{Kind: "bad", Version: 1, Capability: "test.read", Risk: RiskRead, Location: LocationCloud, Idempotent: true, InputSchema: JSONSchema{"type": "object"}, OutputSchema: JSONSchema{"type": "object"}, HealthCheck: func(context.Context) error { return nil }, Cancel: func(context.Context, Invocation) error { return nil }, Execute: func(context.Context, Invocation) (json.RawMessage, error) { return json.RawMessage(`[]`), nil }})
	definition := Definition{FormatVersion: 2, Inputs: JSONSchema{"type": "object"}, Outputs: JSONSchema{"type": "object"}, Capabilities: []CapabilityRequirement{{Capability: "test.read", Risk: RiskRead}}, Nodes: []Node{{ID: "bad", Kind: "bad", KindVersion: 1, Config: json.RawMessage(`{}`), OutputSchema: JSONSchema{"type": "object"}, Retry: DefaultRetryPolicy(), Errors: ErrorPolicy{Mode: "fail"}}}, Dependencies: []WorkflowDependency{}}
	result, err := (Engine{Registry: registry}).Execute(context.Background(), definition, ExecutionRequest{RunID: "run-2", Input: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrOutputInvalid) || result.State != RunFailed {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestEngineRecordsSuccessfulAttemptAndValidatesStructuredOutput(t *testing.T) {
	registry := NewRegistry()
	attempts := 0
	_ = registry.Register(NodeDescriptor{Kind: "structured", Version: 1, Capability: "test.read", Risk: RiskRead, Location: LocationCloud, Idempotent: true, InputSchema: JSONSchema{"type": "object"}, OutputSchema: JSONSchema{"type": "object"}, HealthCheck: func(context.Context) error { return nil }, Cancel: func(context.Context, Invocation) error { return nil }, Execute: func(context.Context, Invocation) (json.RawMessage, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("temporary")
		}
		return json.RawMessage(`{"text":"grounded"}`), nil
	}})
	definition := Definition{FormatVersion: 2, Inputs: JSONSchema{"type": "object"}, Outputs: JSONSchema{"type": "object"}, Capabilities: []CapabilityRequirement{{Capability: "test.read", Risk: RiskRead}}, Nodes: []Node{{ID: "structured", Kind: "structured", KindVersion: 1, Config: json.RawMessage(`{}`), OutputSchema: JSONSchema{"type": "object", "required": []string{"text"}, "properties": JSONSchema{"text": JSONSchema{"type": "string", "minLength": 1}}, "additionalProperties": false}, Retry: DefaultRetryPolicy(), Errors: ErrorPolicy{Mode: "fail"}}}, Dependencies: []WorkflowDependency{}}
	completedAttempt := 0
	result, err := (Engine{Registry: registry, Cooldown: func(context.Context, StepEvent, int) error { return nil }, Checkpoint: func(_ context.Context, event StepEvent) error {
		if event.State == StepCompleted {
			completedAttempt = event.Attempt
		}
		return nil
	}}).Execute(context.Background(), definition, ExecutionRequest{RunID: "run-structured", Input: json.RawMessage(`{}`)})
	if err != nil || result.State != RunCompleted || completedAttempt != 2 {
		t.Fatalf("result=%#v err=%v completedAttempt=%d", result, err, completedAttempt)
	}
}

func TestEngineResumesFromSchemaValidatedCompletedOutputs(t *testing.T) {
	registry := NewRegistry()
	executions := 0
	_ = registry.Register(NodeDescriptor{Kind: "resume", Version: 1, Capability: "test.read", Risk: RiskRead, Location: LocationCloud, Idempotent: true, InputSchema: JSONSchema{"type": "object"}, OutputSchema: JSONSchema{"type": "object", "required": []string{"value"}}, HealthCheck: func(context.Context) error { return nil }, Cancel: func(context.Context, Invocation) error { return nil }, Execute: func(context.Context, Invocation) (json.RawMessage, error) {
		executions++
		return json.RawMessage(`{"value":"new"}`), nil
	}})
	definition := Definition{FormatVersion: 2, Inputs: JSONSchema{"type": "object"}, Outputs: JSONSchema{"type": "object"}, Capabilities: []CapabilityRequirement{{Capability: "test.read", Risk: RiskRead}}, Nodes: []Node{{ID: "done", Kind: "resume", KindVersion: 1, Config: json.RawMessage(`{}`), OutputSchema: JSONSchema{"type": "object", "required": []string{"value"}}, Retry: DefaultRetryPolicy(), Errors: ErrorPolicy{Mode: "fail"}}}}
	result, err := (Engine{Registry: registry}).Execute(context.Background(), definition, ExecutionRequest{RunID: "run-resume", Input: json.RawMessage(`{}`), Completed: map[string]json.RawMessage{"done": json.RawMessage(`{"value":"checkpoint"}`)}})
	if err != nil || executions != 0 || string(result.Outputs["done"]) != `{"value":"checkpoint"}` {
		t.Fatalf("result=%#v err=%v executions=%d", result, err, executions)
	}
}

func TestForEachRunsBoundedChildGraphAndCollectsItemErrors(t *testing.T) {
	registry := NewRegistry()
	registerTestNode := func(kind, capability string, execute func(context.Context, Invocation) (json.RawMessage, error)) {
		t.Helper()
		if err := registry.Register(NodeDescriptor{Kind: kind, Version: 1, Capability: capability, Risk: RiskRead, Location: LocationCloud, Idempotent: true, InputSchema: JSONSchema{"type": "object"}, OutputSchema: JSONSchema{"type": "object"}, HealthCheck: func(context.Context) error { return nil }, Cancel: func(context.Context, Invocation) error { return nil }, Execute: execute}); err != nil {
			t.Fatal(err)
		}
	}
	registerTestNode("for_each", "workflow.control", func(context.Context, Invocation) (json.RawMessage, error) {
		return nil, errors.New("engine should own for_each")
	})
	registerTestNode("process", "content.read", func(_ context.Context, invocation Invocation) (json.RawMessage, error) {
		var input map[string]any
		_ = json.Unmarshal(invocation.Input, &input)
		if input["fail"] == true {
			return nil, errors.New("bad item")
		}
		return json.RawMessage(`{"ok":true}`), nil
	})
	child := Definition{FormatVersion: 2, Inputs: JSONSchema{"type": "object"}, Outputs: JSONSchema{"type": "object"}, Capabilities: []CapabilityRequirement{{Capability: "content.read", Risk: RiskRead}}, Nodes: []Node{{ID: "process", Kind: "process", KindVersion: 1, Config: json.RawMessage(`{}`), OutputSchema: JSONSchema{"type": "object"}, Retry: DefaultRetryPolicy(), Errors: ErrorPolicy{Mode: "fail"}}}, Dependencies: []WorkflowDependency{}}
	config, _ := json.Marshal(TestingForEachConfig{ChildGraph: &child, Concurrency: 2, MaximumItems: 10, ErrorMode: "collect"})
	root := Definition{FormatVersion: 2, Inputs: JSONSchema{"type": "object"}, Outputs: JSONSchema{"type": "object"}, Capabilities: []CapabilityRequirement{{Capability: "workflow.control", Risk: RiskRead}}, Nodes: []Node{{ID: "loop", Kind: "for_each", KindVersion: 1, Config: config, OutputSchema: JSONSchema{"type": "object"}, Retry: DefaultRetryPolicy(), Errors: ErrorPolicy{Mode: "collect", AcceptsPartial: true}}}, Dependencies: []WorkflowDependency{}}
	itemCheckpoints := 0
	result, err := (Engine{Registry: registry, Cooldown: func(context.Context, StepEvent, int) error { return nil }, ItemCheckpoint: func(context.Context, string, json.RawMessage, ExecutionResult, error) error {
		itemCheckpoints++
		return nil
	}}).Execute(context.Background(), root, ExecutionRequest{RunID: "run-loop", Input: json.RawMessage(`{"items":[{"id":1},{"id":2,"fail":true},{"id":3}]}`)})
	if err != nil || result.State != RunCompletedWithErrors || result.Errors["loop"] == "" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	var output struct {
		Items   []any             `json:"items"`
		Errors  map[string]string `json:"errors"`
		Partial bool              `json:"partial"`
	}
	if json.Unmarshal(result.Outputs["loop"], &output) != nil || len(output.Items) != 3 || !output.Partial || output.Errors["1"] == "" || itemCheckpoints != 3 {
		t.Fatalf("loop output=%s", result.Outputs["loop"])
	}
}

func TestEngineSuspendsImmediatelyForPerActionApproval(t *testing.T) {
	registry := NewRegistry()
	attempts := 0
	_ = registry.Register(NodeDescriptor{Kind: "delete_resource", Version: 1, Capability: "resources.delete", Risk: RiskDestructive, Location: LocationCloud, Idempotent: true, InputSchema: JSONSchema{"type": "object"}, OutputSchema: JSONSchema{"type": "object"}, HealthCheck: func(context.Context) error { return nil }, Cancel: func(context.Context, Invocation) error { return nil }, Execute: func(context.Context, Invocation) (json.RawMessage, error) {
		attempts++
		return nil, ErrAwaitingApproval
	}})
	definition := Definition{FormatVersion: 2, Inputs: JSONSchema{"type": "object"}, Outputs: JSONSchema{"type": "object"}, Capabilities: []CapabilityRequirement{{Capability: "resources.delete", Risk: RiskDestructive}}, Nodes: []Node{{ID: "delete", Kind: "delete_resource", KindVersion: 1, Config: json.RawMessage(`{}`), OutputSchema: JSONSchema{"type": "object"}, Retry: DefaultRetryPolicy(), Errors: ErrorPolicy{Mode: "fail"}}}, Dependencies: []WorkflowDependency{}}
	result, err := (Engine{Registry: registry}).Execute(context.Background(), definition, ExecutionRequest{RunID: "run-approval", Input: json.RawMessage(`{"resourceId":"item-1","fingerprint":"v1"}`)})
	if !errors.Is(err, ErrAwaitingApproval) || result.State != RunAwaitingApproval || attempts != 1 {
		t.Fatalf("result=%#v attempts=%d err=%v", result, attempts, err)
	}
}

func TestResolveNodeInputUsesTypedSourcePortsAndNestedRunPaths(t *testing.T) {
	definition := Definition{Edges: []Edge{{ID: "edge", Source: "source", SourcePort: "result.items", Target: "target", TargetPort: "items"}}}
	node := Node{ID: "target", Inputs: map[string]Binding{"account": {InputPath: "trigger.account.id"}}}
	input, err := TestingResolveNodeInput(definition, node, json.RawMessage(`{"trigger":{"account":{"id":"acct-1"}}}`), map[string]json.RawMessage{"source": json.RawMessage(`{"result":{"items":[1,2]}}`)}, map[string]bool{}, map[string]bool{})
	if err != nil || string(input) != `{"account":"acct-1","items":[1,2]}` {
		t.Fatalf("input=%s err=%v", input, err)
	}
}

func TestEngineSkipsInactiveConditionalBranch(t *testing.T) {
	registry := NewRegistry()
	executed := 0
	register := func(kind string, execute func(context.Context, Invocation) (json.RawMessage, error)) {
		t.Helper()
		if err := registry.Register(NodeDescriptor{Kind: kind, Version: 1, Capability: "workflow.control", Risk: RiskRead, Location: LocationCloud, Idempotent: true, InputSchema: JSONSchema{"type": "object"}, OutputSchema: JSONSchema{"type": "object"}, HealthCheck: func(context.Context) error { return nil }, Cancel: func(context.Context, Invocation) error { return nil }, Execute: execute}); err != nil {
			t.Fatal(err)
		}
	}
	register("condition", func(context.Context, Invocation) (json.RawMessage, error) {
		return json.RawMessage(`{"matched":true,"true":{"ok":true}}`), nil
	})
	register("action", func(context.Context, Invocation) (json.RawMessage, error) {
		executed++
		return json.RawMessage(`{"done":true}`), nil
	})
	definition := Definition{FormatVersion: 2, Inputs: JSONSchema{"type": "object"}, Outputs: JSONSchema{"type": "object"}, Capabilities: []CapabilityRequirement{{Capability: "workflow.control", Risk: RiskRead}}, Nodes: []Node{
		{ID: "condition", Kind: "condition", KindVersion: 1, Config: json.RawMessage(`{}`), OutputSchema: JSONSchema{"type": "object"}, Retry: DefaultRetryPolicy(), Errors: ErrorPolicy{Mode: "fail"}},
		{ID: "false_action", Kind: "action", KindVersion: 1, Config: json.RawMessage(`{}`), OutputSchema: JSONSchema{"type": "object"}, Retry: DefaultRetryPolicy(), Errors: ErrorPolicy{Mode: "fail"}},
	}, Edges: []Edge{{ID: "false-branch", Source: "condition", SourcePort: "false", Target: "false_action", TargetPort: "input"}}, Dependencies: []WorkflowDependency{}}
	result, err := (Engine{Registry: registry}).Execute(context.Background(), definition, ExecutionRequest{RunID: "run-branch", Input: json.RawMessage(`{}`)})
	if err != nil || result.State != RunCompleted || executed != 0 {
		t.Fatalf("result=%#v executed=%d err=%v", result, executed, err)
	}
}
