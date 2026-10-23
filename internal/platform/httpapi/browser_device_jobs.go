package api

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/internal/workflow"
)

// queueBrowserDeviceJob queues the device job for a browser tool. A canceled
// job that never started is re-armed so the tool can still run.
func (s *SpacesService) queueBrowserDeviceJob(
	ctx context.Context,
	invocation agenttools.Invocation,
	tool agent.ToolRequest,
	scopeID string,
	schema json.RawMessage,
	configData map[string]any,
) (*db.WorkflowDeviceNodeJob, error) {
	config := MustAPIRawJSON(configData)
	var job *db.WorkflowDeviceNodeJob
	var err error
	if isAIInvocationRuntimeID(invocation.RunID) {
		job, err = s.database.QueueAIInvocationDeviceNodeJob(
			ctx, invocation.UserID, invocation.RunID, "browser_tool_"+tool.ID, 1,
			scopeID, tool.Name, tool.Name, tool.Arguments, config,
			schema, agentToolObjectOutputSchema(),
		)
	} else {
		job, err = s.database.QueueWorkflowDeviceNodeJob(
			ctx, invocation.UserID, invocation.RunID, "browser_tool_"+tool.ID, 1,
			scopeID, tool.Name, tool.Name, tool.Arguments, config,
			schema, agentToolObjectOutputSchema(),
		)
	}
	if errors.Is(err, db.ErrDeviceNotFound) {
		return nil, errors.Join(db.ErrAgentToolboxNotAttempted, workflow.ErrDeviceUnavailable)
	}
	if err != nil {
		return nil, err
	}
	if job.State == "canceled" && job.ControlVersion == 2 && job.ExecutionStartedAt == nil {
		job, err = s.database.RearmUnstartedBrowserJob(ctx, invocation.UserID, job)
		if err != nil {
			return nil, errors.Join(db.ErrAgentToolboxNotAttempted, err)
		}
	}
	return job, nil
}

// awaitBrowserDeviceJob waits for a queued device job and maps its final state to the tool result.
func (s *SpacesService) awaitBrowserDeviceJob(
	ctx context.Context,
	invocation agenttools.Invocation,
	tool agent.ToolRequest,
	job *db.WorkflowDeviceNodeJob,
) (json.RawMessage, error) {
	current, err := waitForDeviceJob(ctx, s.database, invocation.UserID, job.ID, job.DeadlineAt)
	if err != nil {
		return s.stopBrowserDeviceTool(invocation.UserID, job.ID, tool.Name)
	}
	switch current.State {
	case "completed":
		if tool.Name == "browser.extract" {
			return s.browserExtract(ctx, invocation, current.ID, tool.Arguments, current.Output)
		}
		return current.Output, nil
	case "uncertain":
		if browserObservation(tool.Name) {
			return nil, browserObservationFailure(current.ErrorCode)
		}
		return nil, db.ErrAgentToolboxActionUnknown
	case "canceled":
		return nil, errors.Join(db.ErrAgentToolboxNotAttempted, workflow.ErrDeviceUnavailable)
	default:
		return nil, browserDeviceFailureForOperation(current.ErrorCode, current.Operation)
	}
}

// withJoinedContexts adds the run's stored browser tabs that its request did not
// carry, such as the agent's own tab opened mid-run, so their task and surface resolve.
func (s *SpacesService) withJoinedContexts(ctx context.Context, record *db.AIInvocationRecord, body *aiInvocationInput) error {
	stored, err := s.database.AIInvocationContexts(ctx, record.UserID, record.ID)
	if err != nil {
		return err
	}
	for _, item := range stored {
		if item.Kind != "browser_tab" {
			continue
		}
		known := false
		for _, carried := range body.DeviceContexts {
			if carried.OpaqueRef == item.OpaqueRef && carried.DeviceID == item.DeviceID {
				known = true
				break
			}
		}
		if !known {
			body.DeviceContexts = append(body.DeviceContexts, aiInvocationDeviceContext{
				DeviceID: item.DeviceID, Kind: item.Kind, OpaqueRef: item.OpaqueRef, DisplayName: item.DisplayName,
				Capabilities: item.Capabilities, Metadata: item.Metadata,
			})
		}
	}
	return nil
}
