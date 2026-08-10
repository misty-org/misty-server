package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

// executeLeasedDeviceNode creates an exact, schema-bound node job. A healthy
// device must already exist; otherwise the attempt fails immediately and the
// engine applies its ordinary three-attempt/cooldown policy. The bounded poll
// is only a waiter over durable state—the lease and completion survive a
// coordinator restart and duplicate completions are idempotent.
func (s *SpacesService) executeLeasedDeviceNode(ctx context.Context, run *db.SpaceRun, descriptor workflowv2.NodeDescriptor, invocation workflowv2.Invocation) (json.RawMessage, error) {
	scopeID := workflowScopeID(invocation.Config, invocation.Input)
	if scopeID == "" {
		return nil, workflowv2.ErrOutputInvalid
	}
	inputSchema, _ := json.Marshal(descriptor.InputSchema)
	outputSchema, _ := json.Marshal(descriptor.OutputSchema)
	job, err := s.database.QueueWorkflowDeviceNodeJob(ctx, run.RequestingMemberID, run.ID, invocation.NodeID, invocation.Attempt, scopeID, descriptor.Kind, descriptor.Capability, invocation.Input, invocation.Config, inputSchema, outputSchema)
	if errors.Is(err, db.ErrDeviceNotFound) {
		return nil, workflowv2.ErrDeviceUnavailable
	}
	if err != nil {
		return nil, err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(2 * time.Minute)
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout.C:
			return nil, workflowv2.ErrDeviceUnavailable
		case <-ticker.C:
			current, lookupErr := s.database.WorkflowDeviceNodeJob(ctx, run.RequestingMemberID, job.ID)
			if lookupErr != nil {
				return nil, lookupErr
			}
			switch current.State {
			case "completed":
				return current.Output, nil
			case "failed", "canceled":
				if current.ErrorCode == "device_unavailable" {
					return nil, workflowv2.ErrDeviceUnavailable
				}
				return nil, errors.New("device node execution failed: " + current.ErrorCode)
			}
		}
	}
}

func workflowScopeID(values ...json.RawMessage) string {
	for _, raw := range values {
		var value any
		if json.Unmarshal(raw, &value) == nil {
			if found := findWorkflowScopeID(value); found != "" {
				return found
			}
		}
	}
	return ""
}

func findWorkflowScopeID(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if scope, _ := typed["scopeId"].(string); len(scope) >= 8 {
			return scope
		}
		for _, child := range typed {
			if found := findWorkflowScopeID(child); found != "" {
				return found
			}
		}
	case []any:
		for _, child := range typed {
			if found := findWorkflowScopeID(child); found != "" {
				return found
			}
		}
	}
	return ""
}
