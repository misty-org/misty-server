package api

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/internal/workflow"
)

// browserToolScopeID reads the scope a browser tool acts on.
func browserToolScopeID(raw json.RawMessage) (string, error) {
	var input struct {
		ScopeID string `json:"scopeId"`
	}
	if json.Unmarshal(raw, &input) != nil || len(input.ScopeID) < 8 {
		return "", db.ErrSpaceInvalid
	}
	return input.ScopeID, nil
}

// browserToolSchema returns the input schema of a known browser tool.
func browserToolSchema(name string) (json.RawMessage, error) {
	var schema json.RawMessage
	for _, descriptor := range browserToolDescriptors() {
		if descriptor.Name == name {
			schema = descriptor.InputSchema
			break
		}
	}
	if len(schema) == 0 {
		return nil, workflow.ErrCapabilityDenied
	}
	return schema, nil
}

// browserToolConfig starts the device config with the agent and the account's site blocks.
func (s *SpacesService) browserToolConfig(ctx context.Context, invocation agenttools.Invocation) map[string]any {
	agentID := invocation.AgentID
	if agentID == "" {
		agentID = "misty-unified"
	}
	configData := map[string]any{"agentId": agentID}
	// The device refuses to act on these sites before any input is sent.
	if settings, _, err := s.database.AISettings(ctx, invocation.UserID); err == nil {
		configData["siteBlocks"] = map[string]any{"sensitive": settings.AgentBlockSensitive, "sites": settings.AgentBlockedSites}
		configData["confirmKinds"] = browserConfirmKinds()
		configData["grantSet"] = s.browserGrantSet(ctx, invocation)
	}
	return configData
}

// browserToolSurface reports where a workspace scope runs: the Misty window, or the desktop.
func browserToolSurface(body aiInvocationInput, scopeID string) string {
	// A goal on a workspace scope runs on the Misty window, or on the desktop
	// with Misty's own cursor; either needs the same foreground task.
	surface := "browser"
	for _, target := range body.DeviceContexts {
		var metadata map[string]any
		_ = json.Unmarshal(target.Metadata, &metadata)
		if target.OpaqueRef == scopeID && metadata["workspace_control"] == true {
			surface = "workspace"
			if metadata["desktop_control"] == true {
				surface = "desktop"
			}
		}
	}
	return surface
}

// applyAIInvocationBrowserConfig adds the surface, task, agent and upload details of an
// AI invocation run to configData, and rejects tools the invocation may not use.
func (s *SpacesService) applyAIInvocationBrowserConfig(
	ctx context.Context,
	invocation agenttools.Invocation,
	tool agent.ToolRequest,
	scopeID string,
	configData map[string]any,
) error {
	record, lookupErr := s.database.AIInvocationByID(ctx, invocation.UserID, invocation.RunID)
	if lookupErr != nil {
		return lookupErr
	}
	var body aiInvocationInput
	_ = json.Unmarshal(record.RequestPayload, &body)
	// A tab that joined the run mid-run (screen_open) is only in the run's stored contexts.
	if err := s.withJoinedContexts(ctx, record, &body); err != nil {
		return err
	}
	surface := browserToolSurface(body, scopeID)
	if strings.HasPrefix(tool.Name, "browser.workspace.") || (tool.Name == screenActTool && surface != "browser") {
		if body.ExecutionMode != "agent" || body.WindowLabel != "main" || body.TaskID == "" || surface == "browser" {
			return db.ErrSpaceForbidden
		}
	}
	if tool.Name == screenActTool {
		configData["surface"] = surface
	}
	configData["taskId"] = scopeTaskID(body, scopeID)
	if body.AgentID != "" {
		configData["agentId"] = body.AgentID
	}
	if tool.Name == "browser.upload" {
		return s.applyBrowserUploadConfig(ctx, invocation, record.ConversationID, body, tool.Arguments, configData)
	}
	return nil
}

// applyBrowserUploadConfig checks an upload source and adds it to configData.
func (s *SpacesService) applyBrowserUploadConfig(
	ctx context.Context,
	invocation agenttools.Invocation,
	conversationID string,
	body aiInvocationInput,
	raw json.RawMessage,
	configData map[string]any,
) error {
	upload, err := parseBrowserUploadSource(raw)
	if err != nil {
		return err
	}
	if upload.DownloadID != "" {
		// Download reuse is restricted to a live personal-agent task. The
		// native boundary also verifies task ownership and pinned bytes.
		if body.AgentID == "" || body.TaskID == "" {
			return db.ErrSpaceForbidden
		}
		if err := authorizeNativeAgentBrowserScope(ctx, s.database, invocation, upload.SourceScopeID); err != nil {
			return err
		}
		configData["downloadUpload"] = upload
		return nil
	}
	file, lookupErr := s.database.AIConversationAttachment(ctx, invocation.UserID, upload.AttachmentID)
	if lookupErr != nil || file.LifecycleState != "ready" || file.ConversationID != conversationID || file.InvocationID == "" {
		return db.ErrSpaceForbidden
	}
	configData["upload"] = map[string]any{"id": file.ID, "name": file.DisplayName, "mimeType": file.MIMEType, "byteSize": file.ByteSize, "sha256": file.SHA256}
	return nil
}

// validateBrowserToolArguments rejects a bad extract or upload request before the device acts.
func validateBrowserToolArguments(tool agent.ToolRequest) error {
	if tool.Name == "browser.extract" {
		// Reject a bad instruction or schema before the device reads the page.
		if _, _, err := validBrowserExtractRequest(tool.Arguments); err != nil {
			return err
		}
	}
	if tool.Name == "browser.upload" && !validBrowserUploadTarget(tool.Arguments) {
		return db.ErrSpaceInvalid
	}
	return nil
}

// approveBrowserPageSteps returns the waiting result while page steps need approval.
func (s *SpacesService) approveBrowserPageSteps(
	ctx context.Context,
	invocation agenttools.Invocation,
	tool agent.ToolRequest,
	scopeID string,
) (json.RawMessage, error) {
	approval, waiting, err := s.browserPageStepsApproval(ctx, invocation, tool, scopeID)
	if err != nil {
		return nil, err
	}
	if waiting != nil {
		return waiting, nil
	}
	return nil, s.useAgentActionApproval(ctx, invocation.UserID, approval)
}

// consequentialGoal names what a screen goal may only do with the person's approval.
var consequentialGoal = regexp.MustCompile(`(?i)\b(buy|purchas|pay (for|the|now|with|it)|payment|checkout|check out|order|book(ing)?|reserve|send|e-?mail|message|post|publish|tweet|share|invite|delete|remove|cancel|unsubscribe|subscribe|sign ?up|register|apply for|transfer|donate|submit (an? )?(application|order|payment|form))`)

// withoutNeedlessConsequence clears allowConsequential on a screen goal that names nothing
// consequential: a model set it on "enter 55500 and click Calculate" and on a date filter,
// and each time the run stopped on an approval card. With it cleared the screen agent still
// stops before anything consequential it meets, so nothing the person must approve is done.
func withoutNeedlessConsequence(tool agent.ToolRequest) agent.ToolRequest {
	var args map[string]any
	if json.Unmarshal(tool.Arguments, &args) != nil || args["allowConsequential"] != true {
		return tool
	}
	goal, _ := args["goal"].(string)
	if consequentialGoal.MatchString(goal) {
		return tool
	}
	args["allowConsequential"] = false
	if raw, err := json.Marshal(args); err == nil {
		tool.Arguments = raw
	}
	return tool
}

// approveScreenAct pauses on-screen actions: every one in Ask, and in both
// modes the consequential ones, which act on the whole screen and can never be
// remembered. It returns the waiting result while the approval is pending.
func (s *SpacesService) approveScreenAct(
	ctx context.Context,
	invocation agenttools.Invocation,
	tool agent.ToolRequest,
	scopeID string,
) (json.RawMessage, error) {
	var act struct {
		Goal               string `json:"goal"`
		AllowConsequential bool   `json:"allowConsequential"`
	}
	_ = json.Unmarshal(tool.Arguments, &act)
	need := grantNeed{action: agent.ActionToolWrite}
	if act.AllowConsequential {
		need.action = agent.ActionScreenControl
	}
	approval, waiting, err := s.confirmAgentAction(ctx, invocation, need, "screen.act:"+scopeID, act.Goal,
		"On screen: "+truncateAgentRuntimeText(act.Goal, 180), act.Goal)
	if err != nil {
		return nil, err
	}
	if waiting != nil {
		return waiting, nil
	}
	return nil, s.useAgentActionApproval(ctx, invocation.UserID, approval)
}
