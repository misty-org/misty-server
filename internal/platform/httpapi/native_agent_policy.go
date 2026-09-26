package api

import (
	"context"
	"encoding/json"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"strings"
)

func nativeAgentManagementTool(name string) bool {
	return strings.HasPrefix(name, "agents.") || strings.HasPrefix(name, "memory.")
}

func nativeAgentToolAllowed(name, risk, mode string, tools map[string]bool) bool {
	if strings.HasPrefix(name, "browser.workspace.") {
		return mode == "agent" && tools["browser"]
	}
	if strings.HasPrefix(name, "spaces.") {
		return true
	}
	if strings.HasPrefix(name, "agents.") {
		return true
	}
	if strings.HasPrefix(name, "memory.") {
		return true
	}
	if name == "ask.delegate" {
		return false
	}
	if strings.HasPrefix(name, "mcp.") {
		return true
	}
	if strings.HasPrefix(name, "browser.") {
		return tools["browser"]
	}
	if strings.HasPrefix(name, "files.") {
		return tools["files"]
	}
	return name == "weather.current"
}

func nativeAgentInvocationPolicy(ctx context.Context, database *db.Database, invocation agenttools.Invocation, descriptor agenttools.Descriptor) (bool, bool, error) {
	if database == nil {
		return false, false, nil
	}
	if !isAIInvocationRuntimeID(invocation.RunID) {
		if !strings.HasPrefix(invocation.RunID, "run_") || invocation.AgentID == "" {
			return false, false, nil
		}
		run, err := database.SpaceRun(ctx, invocation.UserID, invocation.RunID)
		if err != nil {
			return true, false, err
		}
		if run.AgentID != invocation.AgentID || run.OwnerUserID != invocation.UserID {
			return true, false, db.ErrSpaceForbidden
		}
		identity, err := database.AskIdentityByID(ctx, invocation.UserID, run.AgentID)
		if err != nil {
			return true, false, err
		}
		if !identity.Enabled {
			return true, false, db.ErrSpaceForbidden
		}
		if descriptor.ProviderBinding != nil {
			allowed, err := authorizeAgentSDKTool(ctx, database, invocation, descriptor)
			return true, allowed, err
		}
		if strings.HasPrefix(descriptor.Name, "mcp.") {
			allowed, err := authorizeMCPAgentTool(ctx, database, invocation, descriptor)
			return true, allowed, err
		}
		// Browser/device descriptors are registered only for live run targets.
		return true, true, nil
	}
	record, err := database.AIInvocationByID(ctx, invocation.UserID, invocation.RunID)
	if err != nil {
		return true, false, err
	}
	var body aiInvocationInput
	if json.Unmarshal(record.RequestPayload, &body) != nil {
		return true, false, db.ErrSpaceInvalid
	}
	if body.AgentID == "" {
		return false, false, nil
	} // Existing admitted runtime records retain their protocol.
	if err := database.ValidateNativeAgentExecution(ctx, record); err != nil {
		return true, false, err
	}
	tools, err := database.AgentWorkspaceTools(ctx, record.UserID, body.AgentID)
	if err != nil {
		return true, false, err
	}
	// Destination tools use the user's existing access. There is no per-agent grant.
	if invocation.SpaceID != "" && globalSpaceTool(descriptor.Name) {
		return true, true, nil
	}
	allowed := map[string]bool{}
	for _, id := range tools {
		allowed[id] = true
	}
	if descriptor.ProviderBinding != nil {
		allowed, err := authorizeAgentSDKTool(ctx, database, invocation, descriptor)
		return true, allowed, err
	}
	if strings.HasPrefix(descriptor.Name, "mcp.") {
		authorized, err := authorizeMCPAgentTool(ctx, database, invocation, descriptor)
		if err != nil {
			return true, false, err
		}
		if !authorized {
			return true, false, nil
		}
		return true, nativeAgentToolAllowed(descriptor.Name, descriptor.Risk, body.ExecutionMode, allowed), nil
	}
	if strings.HasPrefix(descriptor.Name, "browser.workspace.") && (body.ExecutionMode != "agent" || body.WindowLabel != "main" || body.TaskID == "") {
		return true, false, nil
	}
	if strings.HasPrefix(descriptor.Name, "browser.") {
		contexts, err := database.AIInvocationContexts(ctx, record.UserID, record.ID)
		if err != nil {
			return true, false, err
		}
		permitted := false
		for _, item := range contexts {
			var metadata struct {
				AppID string `json:"app_id"`
			}
			if json.Unmarshal(item.Metadata, &metadata) == nil && metadata.AppID == "browser" && allowed["browser"] {
				permitted = true
			}
		}
		if !permitted {
			return true, false, nil
		}
	}
	return true, nativeAgentToolAllowed(descriptor.Name, descriptor.Risk, body.ExecutionMode, allowed), nil
}

func filterNativeAgentContext(refs []aiContextReference, tools []string) []aiContextReference {
	browser := false
	for _, tool := range tools {
		if tool == "browser" {
			browser = true
		}
	}
	result := []aiContextReference{}
	for _, ref := range refs {
		switch ref.Kind {
		case "browser-tab":
			if browser && ref.Metadata["app_id"] == "browser" {
				result = append(result, ref)
			}
		default:
			// Agents inherit the account's content access; the broker resolves
			// sources under the signed-in user rather than a per-agent grant.
			result = append(result, ref)
		}
	}
	return result
}

// Recheck the exact target immediately before dispatch; another assigned scope is not authority.
func authorizeNativeAgentBrowserScope(ctx context.Context, database *db.Database, invocation agenttools.Invocation, scopeID string) error {
	if !isAIInvocationRuntimeID(invocation.RunID) {
		return nil
	}
	record, err := database.AIInvocationByID(ctx, invocation.UserID, invocation.RunID)
	if err != nil {
		return err
	}
	var body aiInvocationInput
	if json.Unmarshal(record.RequestPayload, &body) != nil {
		return db.ErrSpaceInvalid
	}
	if body.AgentID == "" {
		return nil
	}
	tools, err := database.AgentWorkspaceTools(ctx, record.UserID, body.AgentID)
	if err != nil {
		return err
	}
	contexts, err := database.AIInvocationContexts(ctx, record.UserID, record.ID)
	if err != nil {
		return err
	}
	for _, item := range contexts {
		if item.OpaqueRef != scopeID {
			continue
		}
		var metadata struct {
			AppID       string `json:"app_id"`
			WindowLabel string `json:"window_label"`
		}
		if json.Unmarshal(item.Metadata, &metadata) != nil || metadata.WindowLabel != body.WindowLabel {
			return db.ErrSpaceForbidden
		}
		for _, tool := range tools {
			if tool == "browser" && metadata.AppID == "browser" {
				return nil
			}
		}
	}
	return db.ErrSpaceForbidden
}
