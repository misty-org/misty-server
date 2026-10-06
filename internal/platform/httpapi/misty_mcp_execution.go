package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// executeAIInvocationMCPTool runs one chat tool call through the run's catalog.
func (s *SpacesService) executeAIInvocationMCPTool(ctx context.Context, access *mcpRuntimeAccess, call agentRuntimeToolCall) (json.RawMessage, error) {
	if access == nil || access.record == nil || access.prepared == nil || !agentToolNameAllowed(access.prepared.allowedTools, call.Name) {
		return nil, workflowv2.ErrCapabilityDenied
	}
	// Second line for Plan mode: even a stale catalog cannot run a write.
	if access.prepared.collaboration.planning() && (access.prepared.toolbox == nil || len(planModeTools(access.prepared.toolbox, []string{call.Name})) != 1) {
		return nil, serveragent.ErrInvalidRequest("Plan mode is read-only. Put this action in the plan as a step instead.")
	}
	ctx = withAgentExecutionRuntime(ctx, call.RuntimeRunID)
	if call.Name == "browser.request_user_action" {
		if !access.claims.InterventionWaits {
			return nil, db.ErrSpaceForbidden
		}
		return s.requestAIUserAction(ctx, access.record.UserID, access.record.ID, call)
	}
	if strings.HasPrefix(call.Name, "browser.") {
		if err := s.aiBrowserDeviceWait(ctx, access, call, false); err != nil {
			return nil, err
		}
	}
	if access.prepared.toolbox == nil {
		return nil, workflowv2.ErrCapabilityDenied
	}
	bounded, cancel, err := boundedAgentExecutionContext(ctx, s.database, access.record.UserID, access.record.ID)
	if err != nil {
		return nil, err
	}
	defer cancel()
	result, err := executeSpaceAgentToolbox(bounded, access.prepared.toolbox, access.prepared.toolInvocation, s.database, serveragent.ToolRequest{
		ID: call.CallID, Name: call.Name, Arguments: call.Arguments,
	})
	if errors.Is(err, workflowv2.ErrDeviceUnavailable) && errors.Is(err, db.ErrAgentToolboxNotAttempted) {
		return nil, s.aiBrowserDeviceWait(ctx, access, call, true)
	}
	if errors.Is(err, db.ErrAgentToolboxActionUnknown) {
		return TestingMustAPIRawJSON(map[string]any{"status": "uncertain", "effect_id": call.CallID, "reason": "The action may have happened. Review its observed outcome before retrying."}), nil
	}
	if err != nil {
		return nil, err
	}
	_ = s.database.TouchAIInvocationRuntime(ctx, access.record.ID, access.claims.RuntimeRunID)
	return result, nil
}

func mcpRunToolDefinition(descriptor agenttools.Descriptor) *mcp.Tool {
	return mcpToolDefinition(descriptor)
}
