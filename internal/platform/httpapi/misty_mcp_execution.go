package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/internal/workflow"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// executeAIInvocationMCPTool runs one chat tool call through the run's catalog.
func (s *SpacesService) executeAIInvocationMCPTool(ctx context.Context, access *mcpRuntimeAccess, call agentRuntimeToolCall) (json.RawMessage, error) {
	if access == nil || access.record == nil || access.prepared == nil || !agentToolNameAllowed(access.prepared.allowedTools, call.Name) {
		return nil, workflow.ErrCapabilityDenied
	}
	// Second line for Plan mode: even a stale catalog cannot run a write.
	if access.prepared.collaboration.planning() && (access.prepared.toolbox == nil || len(planModeTools(access.prepared.toolbox, []string{call.Name})) != 1) {
		return nil, agent.ErrInvalidRequest("Plan mode is read-only. Put this action in the plan as a step instead.")
	}
	ctx = withPermissionGate(withAgentExecutionRuntime(ctx, call.RuntimeRunID), s.askGate)
	if call.Name == "browser.request_user_action" {
		if !access.claims.InterventionWaits {
			return nil, db.ErrSpaceForbidden
		}
		return s.requestAIUserAction(ctx, access.record.UserID, access.record.ID, call)
	}
	// A tab's tools wait for its device here; browser.run waits on its own job at the session's
	// workspace scope, as screen_open does (browser_run.go).
	if strings.HasPrefix(call.Name, "browser.") && call.Name != browserRunTool {
		if err := s.aiBrowserDeviceWait(ctx, access, call, false); err != nil {
			return nil, err
		}
	}
	if access.prepared.toolbox == nil {
		return nil, workflow.ErrCapabilityDenied
	}
	bounded, cancel, err := boundedAgentExecutionContext(ctx, s.database, access.record.UserID, access.record.ID)
	if err != nil {
		return nil, err
	}
	defer cancel()
	result, err := executeSpaceAgentToolbox(bounded, access.prepared.toolbox, access.prepared.toolInvocation, s.database, agent.ToolRequest{
		ID: call.CallID, Name: call.Name, Arguments: call.Arguments,
	})
	if errors.Is(err, workflow.ErrDeviceUnavailable) && errors.Is(err, db.ErrAgentToolboxNotAttempted) {
		// Another device's session never waits: the person picks another device.
		var body aiInvocationInput
		_ = json.Unmarshal(access.record.RequestPayload, &body)
		var input struct {
			ScopeID string `json:"scopeId"`
		}
		_ = json.Unmarshal(call.Arguments, &input)
		if name, remote := s.remoteScopeDevice(ctx, access.record.UserID, body, input.ScopeID); remote {
			return nil, deviceOfflineError{name: name, reason: "went offline."}
		}
		return nil, s.aiBrowserDeviceWait(ctx, access, call, true)
	}
	if errors.Is(err, db.ErrAgentToolboxActionUnknown) {
		return MustAPIRawJSON(map[string]any{"status": "uncertain", "effect_id": call.CallID, "reason": "The action may have happened. Review its observed outcome before retrying."}), nil
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
