package workflow

import (
	"context"
	"encoding/json"
	"errors"
)

var (
	ErrDeviceUnavailable  = errors.New("device_unavailable")
	ErrOutputInvalid      = errors.New("node_output_invalid")
	ErrUnsupportedContent = errors.New("unsupported_content_type")
	ErrAwaitingApproval   = errors.New("awaiting_approval")
	errBranchNotSelected  = errors.New("workflow_branch_not_selected")
)

type StepState string

const (
	StepRunning             StepState = "running"
	StepCooldown            StepState = "cooldown"
	StepCompleted           StepState = "completed"
	StepCompletedWithErrors StepState = "completed_with_errors"
	StepAwaitingApproval    StepState = "awaiting_approval"
	StepFailed              StepState = "failed"
)

type StepEvent struct {
	NodeID  string
	State   StepState
	Attempt int
	Input   json.RawMessage
	Output  json.RawMessage
	Error   error
}

type ExecutionRequest struct {
	RunID   string
	UserID  string
	SpaceID string
	Input   json.RawMessage
	// NodePrefix namespaces checkpoint and idempotency identities for bounded
	// child graphs without changing their published node IDs.
	NodePrefix string
	// Completed contains durable, schema-validated node outputs keyed by the
	// fully-prefixed checkpoint node id. Resumed coordinators skip these nodes.
	Completed map[string]json.RawMessage
}

type ExecutionResult struct {
	State   RunState
	Outputs map[string]json.RawMessage
	Errors  map[string]string
}

// Engine executes a validated acyclic workflow. Cooldown is delegated to the
// durable coordinator, which may persist the event and wait, or wake early
// after provider/device health is restored.
type Engine struct {
	Registry       *Registry
	Resolver       DependencyResolver
	Checkpoint     func(context.Context, StepEvent) error
	Cooldown       func(context.Context, StepEvent, int) error
	ItemCheckpoint func(context.Context, string, json.RawMessage, ExecutionResult, error) error
}

func (engine Engine) Execute(ctx context.Context, definition Definition, request ExecutionRequest) (ExecutionResult, error) {
	if err := Validate(definition, engine.Registry, engine.Resolver); err != nil {
		return ExecutionResult{State: RunFailed}, err
	}
	order := topologicalOrder(definition)
	outputs := map[string]json.RawMessage{}
	failures := map[string]string{}
	partials := map[string]bool{}
	skipped := map[string]bool{}
	for _, node := range order {
		executionNodeID := request.NodePrefix + node.ID
		if checkpointed, ok := request.Completed[executionNodeID]; ok {
			descriptor, registered := engine.Registry.Resolve(node.Kind, node.KindVersion)
			if !registered || validateOutput(descriptor.OutputSchema, checkpointed) != nil || validateOutput(node.OutputSchema, checkpointed) != nil {
				return ExecutionResult{State: RunFailed, Outputs: outputs, Errors: failures}, ErrOutputInvalid
			}
			outputs[node.ID] = checkpointed
			continue
		}
		input, err := TestingResolveNodeInput(definition, node, request.Input, outputs, partials, skipped)
		if err != nil {
			if errors.Is(err, errBranchNotSelected) {
				skipped[node.ID] = true
				continue
			}
			failures[node.ID] = err.Error()
			if node.Errors.Mode != "continue" && node.Errors.Mode != "collect" {
				return ExecutionResult{State: RunFailed, Outputs: outputs, Errors: failures}, err
			}
			continue
		}
		descriptor, ok := engine.Registry.Resolve(node.Kind, node.KindVersion)
		if !ok {
			return ExecutionResult{State: RunFailed, Outputs: outputs, Errors: failures}, ErrProviderMissing
		}
		maxAttempts := node.Retry.MaxAttempts
		if maxAttempts == 0 {
			maxAttempts = 3
		}
		var output json.RawMessage
		completedAttempt := 0
		nodePartial := false
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			output = nil
			if ctx.Err() != nil {
				_ = descriptor.Cancel(context.Background(), Invocation{RunID: request.RunID, NodeID: node.ID, Attempt: attempt, UserID: request.UserID, SpaceID: request.SpaceID})
				return ExecutionResult{State: RunCanceled, Outputs: outputs, Errors: failures}, ctx.Err()
			}
			event := StepEvent{NodeID: executionNodeID, State: StepRunning, Attempt: attempt, Input: input}
			if err := engine.checkpoint(ctx, event); err != nil {
				return ExecutionResult{State: RunFailed, Outputs: outputs, Errors: failures}, err
			}
			invocation := Invocation{RunID: request.RunID, NodeID: executionNodeID, Attempt: attempt, IdempotencyKey: idempotencyKey(request.RunID, executionNodeID), UserID: request.UserID, SpaceID: request.SpaceID, Config: node.Config, Input: input}
			err = validateOutput(descriptor.InputSchema, input)
			if err == nil {
				err = descriptor.HealthCheck(ctx)
			}
			if err == nil && descriptor.Risk != RiskRead && attempt > 1 && descriptor.SupportsReconcile && descriptor.Reconcile != nil {
				var reconciled bool
				output, reconciled, err = descriptor.Reconcile(ctx, invocation)
				if err == nil && reconciled {
					err = validateOutput(descriptor.OutputSchema, output)
					if err == nil {
						err = validateOutput(node.OutputSchema, output)
					}
					if err == nil {
						break
					}
				}
			}
			if err == nil && output == nil {
				switch node.Kind {
				case "call_workflow":
					output, err = engine.executeCallWorkflow(ctx, request, invocation)
				case "for_each":
					output, nodePartial, err = engine.executeForEach(ctx, request, invocation)
				default:
					output, err = descriptor.Execute(ctx, invocation)
				}
			}
			if err == nil {
				err = validateOutput(descriptor.OutputSchema, output)
				if err == nil {
					err = validateOutput(node.OutputSchema, output)
				}
			}
			if err == nil {
				completedAttempt = attempt
				break
			}
			if errors.Is(err, ErrAwaitingApproval) {
				_ = engine.checkpoint(ctx, StepEvent{NodeID: executionNodeID, State: StepAwaitingApproval, Attempt: attempt, Input: input, Error: err})
				return ExecutionResult{State: RunAwaitingApproval, Outputs: outputs, Errors: failures}, err
			}
			if attempt < maxAttempts {
				cooldown := StepEvent{NodeID: executionNodeID, State: StepCooldown, Attempt: attempt, Input: input, Error: err}
				if checkpointErr := engine.checkpoint(ctx, cooldown); checkpointErr != nil {
					return ExecutionResult{State: RunFailed, Outputs: outputs, Errors: failures}, checkpointErr
				}
				if engine.Cooldown != nil {
					if cooldownErr := engine.Cooldown(ctx, cooldown, node.Retry.CooldownSeconds); cooldownErr != nil {
						return ExecutionResult{State: RunFailed, Outputs: outputs, Errors: failures}, cooldownErr
					}
				}
			}
		}
		if err != nil {
			failures[node.ID] = err.Error()
			_ = engine.checkpoint(ctx, StepEvent{NodeID: executionNodeID, State: StepFailed, Attempt: maxAttempts, Input: input, Error: err})
			if node.Errors.Mode != "continue" && node.Errors.Mode != "collect" {
				return ExecutionResult{State: RunFailed, Outputs: outputs, Errors: failures}, err
			}
			continue
		}
		outputs[node.ID] = output
		stepState := StepCompleted
		if nodePartial {
			partials[node.ID] = true
			failures[node.ID] = "one or more items failed"
			stepState = StepCompletedWithErrors
		}
		if err := engine.checkpoint(ctx, StepEvent{NodeID: executionNodeID, State: stepState, Attempt: completedAttempt, Input: input, Output: output}); err != nil {
			return ExecutionResult{State: RunFailed, Outputs: outputs, Errors: failures}, err
		}
	}
	state := RunCompleted
	if len(failures) > 0 {
		state = RunCompletedWithErrors
	}
	return ExecutionResult{State: state, Outputs: outputs, Errors: failures}, nil
}

type TestingForEachConfig struct {
	WorkflowVersionID string      `json:"workflowVersionId"`
	ChildGraph        *Definition `json:"childGraph"`
	Concurrency       int         `json:"concurrency"`
	MaximumItems      int         `json:"maximumItems"`
	Sequential        bool        `json:"sequential"`
	ErrorMode         string      `json:"errorMode"`
}

func (engine Engine) executeCallWorkflow(ctx context.Context, parent ExecutionRequest, invocation Invocation) (json.RawMessage, error) {
	var config struct {
		WorkflowVersionID string `json:"workflowVersionId"`
	}
	if json.Unmarshal(invocation.Config, &config) != nil || config.WorkflowVersionID == "" || engine.Resolver == nil {
		return nil, ErrInvalidDefinition
	}
	_, _, child, ok := engine.Resolver.ResolveWorkflowVersion(config.WorkflowVersionID)
	if !ok {
		return nil, ErrInvalidDefinition
	}
	result, err := engine.Execute(ctx, child, ExecutionRequest{RunID: parent.RunID, UserID: parent.UserID, SpaceID: parent.SpaceID, Input: invocation.Input, NodePrefix: invocation.NodeID + ".", Completed: parent.Completed})
	if err != nil {
		return nil, err
	}
	return marshalExecutionResult(result), nil
}
