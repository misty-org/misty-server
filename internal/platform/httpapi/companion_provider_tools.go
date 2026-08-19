package api

import (
	"context"
	"encoding/json"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func (s *SpacesService) companionRunProviders(ctx context.Context, run *db.SpaceRun) []string {
	seen := map[string]bool{}
	providers := []string{}
	if resources, err := s.database.ProviderSharedResources(ctx, run.OwnerUserID, run.SpaceID); err == nil {
		for _, resource := range resources {
			provider := strings.ToLower(strings.TrimSpace(resource.Provider))
			if resource.Status != "active" || seen[provider] || !containsString(canonicalAgentToolboxProviders, provider) {
				continue
			}
			seen[provider] = true
			providers = append(providers, provider)
		}
	}
	if sources, err := s.database.SpaceCalendarSources(ctx, run.OwnerUserID, run.SpaceID); err == nil && len(sources) > 0 && !seen["google"] {
		providers = append(providers, "google")
	}
	return providers
}

// executeCompanionProviderTool relies on the creator-scoped approval gate in
// AgentRuntimeTool. It deliberately bypasses the retired workflow-version
// approval tables while preserving the existing provider adapters and action
// journal as the idempotent side-effect boundary.
func (s *SpacesService) executeCompanionProviderTool(ctx context.Context, run *db.SpaceRun, tool serveragent.ToolRequest) (json.RawMessage, error) {
	parts := strings.Split(tool.Name, ".")
	if len(parts) != 3 || parts[0] != "provider" || !containsString(canonicalAgentToolboxProviders, parts[1]) {
		return nil, agenttools.ErrToolNotFound
	}
	provider, operation := parts[1], parts[2]
	var config map[string]any
	if json.Unmarshal(tool.Arguments, &config) != nil {
		return nil, db.ErrSpaceInvalid
	}
	config["provider"], config["operation"] = provider, operation
	invocation := workflowv2.Invocation{
		RunID: run.ID, NodeID: "companion_tool_" + tool.ID, Attempt: 1,
		IdempotencyKey: "companion:" + run.ID + ":" + tool.ID,
		UserID:         run.OwnerUserID, SpaceID: run.SpaceID, Config: TestingMustAPIRawJSON(config), Input: tool.Arguments,
	}
	if operation == "query" {
		return s.providerQueryNode(ctx, run, invocation)
	}
	if operation != "write" || !providerSupportsWrite(provider) {
		return nil, workflowv2.ErrCapabilityDenied
	}
	return s.database.JournalWorkflowAction(ctx, run.ID, invocation.NodeID, invocation.IdempotencyKey, provider, workflowv2.RiskWrite, tool.Arguments, func() (json.RawMessage, error) {
		return s.providerWriteNode(ctx, run, invocation)
	})
}
