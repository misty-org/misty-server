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

func (s *SpacesService) executeAIInvocationMCPTool(ctx context.Context, access *mcpRuntimeAccess, call agentRuntimeToolCall) (json.RawMessage, error) {
	if access == nil || access.record == nil || access.prepared == nil || !agentToolNameAllowed(access.prepared.allowedTools, call.Name) {
		return nil, workflowv2.ErrCapabilityDenied
	}
	ctx = withAgentExecutionRuntime(ctx, call.RuntimeRunID)
	prepared := access.prepared
	authority, authorityErr := s.database.ExecutionAuthorityForRun(ctx, access.record.ID, access.record.UserID)
	if authorityErr != nil {
		return nil, authorityErr
	}
	if authority != nil {
		permitted := false
		for _, descriptor := range aiInvocationMCPDescriptors(prepared.allowedTools) {
			if descriptor.Name == call.Name {
				permitted = s.database.ValidateAppExecutionAuthority(ctx, authority, access.record.UserID, prepared.spaceID, appScopeForTool(descriptor)) == nil
				break
			}
		}
		if !permitted {
			return nil, db.ErrAppRuntimeForbidden
		}
	}
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
	browserApproved := false
	if call.Name == "browser.click" || call.Name == "browser.interact" || call.Name == "browser.workspace.interact" {
		approval, allowed, err := s.requireAIInvocationBrowserApproval(ctx, access, call)
		if err != nil {
			return nil, err
		}
		if !allowed {
			if approval.State == "pending" {
				return nil, &browserApprovalRequired{approval}
			}
			return TestingMustAPIRawJSON(map[string]any{"denied": true, "reason": "approval_denied_or_expired", "approval_id": approval.ID}), nil
		}
		browserApproved = true
	}
	var result json.RawMessage
	var err error
	if prepared.spaceID == "" || call.Name == toolboxWeatherCurrent {
		bounded, cancel, err := boundedAgentExecutionContext(ctx, s.database, access.record.UserID, access.record.ID)
		if err != nil {
			return nil, err
		}
		defer cancel()
		ctx = bounded
	}
	if call.Name == toolboxWeatherCurrent {
		var input struct {
			Location string `json:"location"`
		}
		if json.Unmarshal(call.Arguments, &input) != nil {
			return nil, db.ErrSpaceInvalid
		}
		result, err = currentWeather(ctx, input.Location)
	} else if call.Name == toolboxContextGet && prepared.spaceID == "" {
		result = TestingMustAPIRawJSON(map[string]any{
			"timezone": prepared.timezone, "current_time": prepared.currentTime.Format("2006-01-02T15:04:05Z07:00"),
			"current_date": prepared.currentTime.Format("2006-01-02"), "scope": "account",
		})
	} else if prepared.spaceID == "" && (call.Name == toolboxMemoryRemember || call.Name == toolboxMemoryForget) {
		result, _, err = executeAgentMemoryTool(ctx, s.database, spaceConversationToolActor{
			userID: access.record.UserID, agentID: prepared.body.AgentID, runID: access.record.ID, sessionID: access.record.ConversationID,
		}, prepared.body.Prompt, serveragent.ToolRequest{ID: call.CallID, Name: call.Name, Arguments: call.Arguments})
	} else {
		actor := spaceConversationToolActor{
			userID: access.record.UserID, spaceID: prepared.spaceID, agentID: prepared.body.AgentID,
			runID: access.record.ID, sessionID: access.record.ConversationID,
		}
		toolbox, invocation, manifest, resolveErr := resolveAIInvocationSpaceToolbox(
			ctx, s.database, actor, prepared.body.Prompt,
			prepared.previousUserPrompt, prepared.previousAgentReply,
		)
		if resolveErr != nil || !agentManifestHasTool(manifest, call.Name) {
			return nil, workflowv2.ErrCapabilityDenied
		}
		if browserApproved {
			invocation.ApprovedTools = map[string]bool{call.Name: true}
		}
		result, err = executeSpaceAgentToolbox(ctx, toolbox, invocation, s.database, serveragent.ToolRequest{
			ID: call.CallID, Name: call.Name, Arguments: call.Arguments,
		})
	}
	if errors.Is(err, workflowv2.ErrDeviceUnavailable) && errors.Is(err, db.ErrAgentToolboxNotAttempted) {
		return nil, s.aiBrowserDeviceWait(ctx, access, call, true)
	}
	if errors.Is(err, db.ErrAgentToolboxActionUnknown) {
		return TestingMustAPIRawJSON(map[string]any{"status": "uncertain", "effect_id": call.CallID, "reason": "The browser action may have happened. Review its observed outcome before retrying."}), nil
	}
	if err != nil {
		return nil, err
	}
	_ = s.database.TouchAIInvocationRuntime(ctx, access.record.ID, access.claims.RuntimeRunID)
	return result, nil
}

func mcpRunToolDefinition(descriptor agenttools.Descriptor) *mcp.Tool {
	definition := mcpToolDefinition(descriptor)
	if descriptor.ProviderBinding != nil {
		definition.OutputSchema = map[string]any{"oneOf": []any{
			map[string]any{"type": "object", "required": []string{"status", "result", "evidence", "partial"}, "properties": map[string]any{"status": map[string]any{"const": "success"}, "result": descriptor.OutputSchema, "evidence": map[string]any{"type": "array"}, "partial": map[string]any{"type": "boolean"}}},
			map[string]any{"type": "object", "required": []string{"status", "effectId", "reason", "evidence"}, "properties": map[string]any{"status": map[string]any{"const": "uncertain"}}},
		}}
	}
	return definition
}

func mcpSDKToolError(err error) *mcp.CallToolResult {
	var intervention *aiInterventionRequired
	if errors.As(err, &intervention) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "User action is required in the original browser target."}}, Meta: mcp.Meta{"misty/intervention_wait": intervention.wait}}
	}
	return mcpToolError(err)
}
