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
	toolboxRoadmapsQuery  = "roadmaps.query"
	toolboxRoadmapsRead   = "roadmaps.read"
	toolboxRoadmapsCreate = "roadmaps.create"
	toolboxRoadmapsUpdate = "roadmaps.update"
)

func roadmapAgentToolDescriptors() []agenttools.Descriptor {
	querySchema := TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string", "maxLength": 500}}, "additionalProperties": false})
	readSchema := TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "maxLength": 200}}, "required": []string{"id"}, "additionalProperties": false})
	createSchema := TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string", "maxLength": 160}, "description": map[string]any{"type": "string", "maxLength": 5000}}, "required": []string{"name"}, "additionalProperties": false})
	updateSchema := TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string", "maxLength": 200}, "name": map[string]any{"type": "string", "maxLength": 160}, "description": map[string]any{"type": "string", "maxLength": 5000}}, "required": []string{"id"}, "additionalProperties": false})
	base := agenttools.Descriptor{Version: 1, OutputSchema: agentToolObjectOutputSchema(), AllowCustomAgent: true, Locality: agenttools.LocalityServer, Sources: agentToolboxSpaceSources}
	query := base
	query.Name, query.Description, query.Risk, query.InputSchema, query.RequiredPermission, query.AgentPermission, query.Approval, query.Idempotent = toolboxRoadmapsQuery, "List or search roadmaps visible in the current Space.", serveragent.RiskRead, querySchema, db.PermissionTasksView, db.PermissionTasksView, agenttools.ApprovalNone, true
	read := base
	read.Name, read.Description, read.Risk, read.InputSchema, read.RequiredPermission, read.AgentPermission, read.Approval, read.Idempotent = toolboxRoadmapsRead, "Read a roadmap and its milestones, goals, nodes, and progress.", serveragent.RiskRead, readSchema, db.PermissionTasksView, db.PermissionTasksView, agenttools.ApprovalNone, true
	create := base
	create.Name, create.Description, create.Risk, create.InputSchema, create.RequiredPermission, create.AgentPermission, create.Approval, create.AuditEvent = toolboxRoadmapsCreate, "Create a roadmap in the current Space.", serveragent.RiskWrite, createSchema, db.PermissionTasksManage, db.PermissionTasksManage, agenttools.ApprovalExplicitIntent, "roadmap.created"
	update := base
	update.Name, update.Description, update.Risk, update.InputSchema, update.RequiredPermission, update.AgentPermission, update.Approval, update.Idempotent, update.AuditEvent = toolboxRoadmapsUpdate, "Update an explicitly identified roadmap.", serveragent.RiskWrite, updateSchema, db.PermissionTasksManage, db.PermissionTasksManage, agenttools.ApprovalExplicitIntent, true, "roadmap.updated"
	return []agenttools.Descriptor{query, read, create, update}
}

func executeAgentRoadmapTool(ctx context.Context, database *db.Database, actor spaceConversationToolActor, tool serveragent.ToolRequest) (json.RawMessage, bool, error) {
	switch tool.Name {
	case toolboxRoadmapsQuery:
		var input struct {
			Query string `json:"query"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil {
			return nil, true, serveragent.ErrInvalidRequest("roadmap query is invalid")
		}
		items, err := database.SpaceRoadmaps(ctx, actor.userID, actor.spaceID)
		if err != nil {
			return nil, true, err
		}
		query := strings.ToLower(strings.TrimSpace(input.Query))
		matches := make([]db.SpaceRoadmap, 0, len(items))
		for _, item := range items {
			if query == "" || strings.Contains(strings.ToLower(item.Name+" "+item.Description), query) {
				matches = append(matches, item)
			}
		}
		return TestingMustAPIRawJSON(map[string]any{"roadmaps": matches, "count": len(matches)}), true, nil
	case toolboxRoadmapsRead:
		var input struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil || strings.TrimSpace(input.ID) == "" {
			return nil, true, serveragent.ErrInvalidRequest("roadmap id is required")
		}
		item, err := database.SpaceRoadmap(ctx, actor.userID, actor.spaceID, input.ID)
		return TestingMustAPIRawJSON(item), true, err
	case toolboxRoadmapsCreate:
		var input struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil || strings.TrimSpace(input.Name) == "" {
			return nil, true, serveragent.ErrInvalidRequest("roadmap name is required")
		}
		item, err := database.CreateSpaceRoadmap(ctx, actor.userID, actor.spaceID, input.Name, input.Description)
		return TestingMustAPIRawJSON(item), true, err
	case toolboxRoadmapsUpdate:
		var input struct {
			ID          string  `json:"id"`
			Name        string  `json:"name"`
			Description *string `json:"description"`
		}
		if json.Unmarshal(tool.Arguments, &input) != nil || strings.TrimSpace(input.ID) == "" {
			return nil, true, serveragent.ErrInvalidRequest("roadmap id is required")
		}
		current, err := database.SpaceRoadmap(ctx, actor.userID, actor.spaceID, input.ID)
		if err != nil {
			return nil, true, err
		}
		name, description := current.Roadmap.Name, current.Roadmap.Description
		if strings.TrimSpace(input.Name) != "" {
			name = input.Name
		}
		if input.Description != nil {
			description = *input.Description
		}
		item, err := database.UpdateSpaceRoadmap(ctx, actor.userID, actor.spaceID, input.ID, name, description, current.Roadmap.GraphVersion)
		return TestingMustAPIRawJSON(item), true, err
	default:
		return nil, false, nil
	}
}
