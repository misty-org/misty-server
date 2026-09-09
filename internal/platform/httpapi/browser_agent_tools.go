package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/browseractions"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func (s *SpacesService) executeBrowserAgentTool(
	ctx context.Context,
	run *db.SpaceRun,
	tool serveragent.ToolRequest,
) (json.RawMessage, error) {
	return s.executeBrowserAgentToolInvocation(ctx, agenttools.Invocation{
		UserID: run.RequestingMemberID, SpaceID: run.SpaceID, AgentID: run.AgentID, RunID: run.ID,
	}, tool)
}

func (s *SpacesService) executeBrowserAgentToolInvocation(
	ctx context.Context,
	invocation agenttools.Invocation,
	tool serveragent.ToolRequest,
) (json.RawMessage, error) {
	var input struct {
		ScopeID string `json:"scopeId"`
	}
	if json.Unmarshal(tool.Arguments, &input) != nil || len(input.ScopeID) < 8 {
		return nil, db.ErrSpaceInvalid
	}
	var schema json.RawMessage
	for _, descriptor := range browserToolDescriptors() {
		if descriptor.Name == tool.Name {
			schema = descriptor.InputSchema
			break
		}
	}
	if len(schema) == 0 {
		return nil, workflowv2.ErrCapabilityDenied
	}
	agentID := invocation.AgentID
	if agentID == "" {
		agentID = "misty-unified"
	}
	config := TestingMustAPIRawJSON(map[string]any{"agentId": agentID})
	var job *db.WorkflowDeviceNodeJob
	var err error
	if isAIInvocationRuntimeID(invocation.RunID) {
		job, err = s.database.QueueAIInvocationDeviceNodeJob(
			ctx, invocation.UserID, invocation.RunID, "browser_tool_"+tool.ID, 1,
			input.ScopeID, tool.Name, tool.Name, tool.Arguments, config,
			schema, agentToolObjectOutputSchema(),
		)
	} else {
		job, err = s.database.QueueWorkflowDeviceNodeJob(
			ctx, invocation.UserID, invocation.RunID, "browser_tool_"+tool.ID, 1,
			input.ScopeID, tool.Name, tool.Name, tool.Arguments, config,
			schema, agentToolObjectOutputSchema(),
		)
	}
	if errors.Is(err, db.ErrDeviceNotFound) {
		return nil, errors.Join(db.ErrAgentToolboxNotAttempted, workflowv2.ErrDeviceUnavailable)
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
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(time.Until(job.DeadlineAt))
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			return s.stopBrowserDeviceTool(invocation.UserID, job.ID)
		case <-timeout.C:
			return s.stopBrowserDeviceTool(invocation.UserID, job.ID)
		case <-ticker.C:
			current, lookupErr := s.database.WorkflowDeviceNodeJob(ctx, invocation.UserID, job.ID)
			if lookupErr != nil {
				return s.stopBrowserDeviceTool(invocation.UserID, job.ID)
			}
			switch current.State {
			case "completed":
				return current.Output, nil
			case "uncertain":
				return nil, db.ErrAgentToolboxActionUnknown
			case "canceled":
				return nil, errors.Join(db.ErrAgentToolboxNotAttempted, workflowv2.ErrDeviceUnavailable)
			case "failed":
				if current.ErrorCode == "browser_snapshot_stale" {
					return nil, browseractions.ErrStale
				}
				if current.ErrorCode == "device_unavailable" || current.ErrorCode == "browser_tab_closed" {
					return nil, workflowv2.ErrDeviceUnavailable
				}
				return nil, errors.New("browser device tool failed: " + current.ErrorCode)
			}
		}
	}
}

func (s *SpacesService) stopBrowserDeviceTool(userID, jobID string) (json.RawMessage, error) {
	job, err := s.database.StopWorkflowDeviceNodeJob(userID, jobID)
	if err != nil {
		return nil, errors.Join(db.ErrAgentToolboxActionUnknown, err)
	}
	switch job.State {
	case "completed":
		return job.Output, nil
	case "canceled":
		return nil, errors.Join(db.ErrAgentToolboxNotAttempted, workflowv2.ErrDeviceUnavailable)
	case "failed":
		return nil, errors.New("browser device tool failed: " + job.ErrorCode)
	default:
		return nil, db.ErrAgentToolboxActionUnknown
	}
}
