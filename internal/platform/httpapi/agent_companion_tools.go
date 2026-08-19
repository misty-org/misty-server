package api

import (
	"context"
	"encoding/json"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const (
	toolboxAgentsList   = "agents.list"
	toolboxAgentsStatus = "agents.status"
)

func companionReadToolDescriptors() []agenttools.Descriptor {
	empty := TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false})
	status := TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{"agentId": map[string]any{"type": "string", "maxLength": 200}, "agentName": map[string]any{"type": "string", "maxLength": 200}}, "additionalProperties": false})
	return []agenttools.Descriptor{
		{Name: toolboxAgentsList, Version: 1, Description: "List the creator's enabled companion Agents available in the current Space.", Risk: serveragent.RiskRead, InputSchema: empty, OutputSchema: agentToolObjectOutputSchema(), AllowCustomAgent: true, Approval: agenttools.ApprovalNone, Locality: agenttools.LocalityServer, Idempotent: true, Sources: agentToolboxSpaceSources},
		{Name: toolboxAgentsStatus, Version: 1, Description: "Check whether one creator-owned companion Agent is available or busy without revealing cross-Space work.", Risk: serveragent.RiskRead, InputSchema: status, OutputSchema: agentToolObjectOutputSchema(), AllowCustomAgent: true, Approval: agenttools.ApprovalNone, Locality: agenttools.LocalityServer, Idempotent: true, Sources: agentToolboxSpaceSources},
	}
}

func executeCompanionReadTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, tool serveragent.ToolRequest) (json.RawMessage, bool, error) {
	if tool.Name != toolboxAgentsList && tool.Name != toolboxAgentsStatus {
		return nil, false, nil
	}
	agents, err := database.AccessiblePersonalAgents(ctx, actor.userID, actor.spaceID)
	if err != nil {
		return nil, true, err
	}
	if tool.Name == toolboxAgentsList {
		items := make([]map[string]any, 0, len(agents))
		for _, agent := range agents {
			availability, availabilityErr := database.PersonalAgentWorkAvailability(ctx, actor.userID, agent.ID)
			if availabilityErr != nil {
				return nil, true, availabilityErr
			}
			items = append(items, map[string]any{"id": agent.ID, "name": agent.Name, "busy": availability.Busy, "queue_count": availability.QueueCount})
		}
		return TestingMustAPIRawJSON(map[string]any{"agents": items, "count": len(items)}), true, nil
	}
	var input struct {
		AgentID   string `json:"agentId"`
		AgentName string `json:"agentName"`
	}
	if json.Unmarshal(tool.Arguments, &input) != nil {
		return nil, true, serveragent.ErrInvalidRequest("Agent status input is invalid")
	}
	targetID := strings.TrimSpace(input.AgentID)
	var targetName string
	for _, agent := range agents {
		if targetID == agent.ID || targetID == "" && strings.EqualFold(agent.Name, strings.TrimSpace(input.AgentName)) {
			if targetID == "" && targetName != "" {
				return nil, true, serveragent.ErrInvalidRequest("Agent name is ambiguous")
			}
			targetID, targetName = agent.ID, agent.Name
		}
	}
	if targetID == "" || targetName == "" {
		return nil, true, db.ErrPersonalAgentNotFound
	}
	availability, err := database.PersonalAgentWorkAvailability(ctx, actor.userID, targetID)
	if err != nil {
		return nil, true, err
	}
	return TestingMustAPIRawJSON(map[string]any{"id": targetID, "name": targetName, "busy": availability.Busy, "active_state": availability.ActiveState, "queue_count": availability.QueueCount}), true, nil
}
