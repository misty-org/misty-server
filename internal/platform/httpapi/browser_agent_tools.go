package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
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
	job, err := s.database.QueueWorkflowDeviceNodeJob(
		ctx, invocation.UserID, invocation.RunID, "browser_tool_"+tool.ID, 1,
		input.ScopeID, tool.Name, tool.Name, tool.Arguments,
		TestingMustAPIRawJSON(map[string]any{"agentId": invocation.AgentID}),
		schema, agentToolObjectOutputSchema(),
	)
	if errors.Is(err, db.ErrDeviceNotFound) {
		return nil, workflowv2.ErrDeviceUnavailable
	}
	if err != nil {
		return nil, err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(5 * time.Minute)
	defer timeout.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout.C:
			return nil, workflowv2.ErrDeviceUnavailable
		case <-ticker.C:
			current, lookupErr := s.database.WorkflowDeviceNodeJob(ctx, invocation.UserID, job.ID)
			if lookupErr != nil {
				return nil, lookupErr
			}
			switch current.State {
			case "completed":
				return current.Output, nil
			case "failed", "canceled":
				if current.ErrorCode == "device_unavailable" || current.ErrorCode == "browser_tab_closed" {
					return nil, workflowv2.ErrDeviceUnavailable
				}
				return nil, errors.New("browser device tool failed: " + current.ErrorCode)
			}
		}
	}
}
