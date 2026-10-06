package api

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// nativeAgentInvocationPolicy reports whether a native agent run may use a
// tool. Agents inherit their owner's account and Space permissions; there is no
// per-agent allowlist. The only extra conditions are physical: browser tools
// need an attached browser, and screen control needs the agent window's task.
// The first result is false when the run is not a native agent run.
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
		// Browser and device descriptors are registered only for live run targets.
		allowed, err := authorizeConnectedAgentTool(ctx, database, invocation, descriptor)
		return true, allowed, err
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
	}
	if err := database.ValidateNativeAgentExecution(ctx, record); err != nil {
		return true, false, err
	}
	if strings.HasPrefix(descriptor.Name, "browser.workspace.") && (body.ExecutionMode != "agent" || body.WindowLabel != "main" || body.TaskID == "") {
		return true, false, nil
	}
	if strings.HasPrefix(descriptor.Name, "browser.") {
		attached, err := invocationHasBrowserContext(ctx, database, record)
		if err != nil || !attached {
			return true, false, err
		}
	}
	allowed, err := authorizeConnectedAgentTool(ctx, database, invocation, descriptor)
	return true, allowed, err
}

// authorizeConnectedAgentTool checks tools that act through an MCP connector.
// Misty's own tools and the owner's connected apps need no check beyond the
// owner's permissions.
func authorizeConnectedAgentTool(ctx context.Context, database *db.Database, invocation agenttools.Invocation, descriptor agenttools.Descriptor) (bool, error) {
	switch {
	}
	return true, nil
}

func invocationHasBrowserContext(ctx context.Context, database *db.Database, record *db.AIInvocationRecord) (bool, error) {
	contexts, err := database.AIInvocationContexts(ctx, record.UserID, record.ID)
	if err != nil {
		return false, err
	}
	for _, item := range contexts {
		var metadata struct {
			AppID string `json:"app_id"`
		}
		if json.Unmarshal(item.Metadata, &metadata) == nil && metadata.AppID == "browser" {
			return true, nil
		}
	}
	return false, nil
}

// Recheck the exact target immediately before dispatch; another attached scope is not authority.
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
		if metadata.AppID == "browser" {
			return nil
		}
	}
	return db.ErrSpaceForbidden
}
