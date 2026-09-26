package api

import (
	"context"
	"encoding/json"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"strings"
)

func nativeAgentToolDescriptors() []agenttools.Descriptor {
	schema := func(properties map[string]any, required []string) json.RawMessage {
		// JSON Schema requires an array. A nil Go slice serializes as null and
		// prevents the runtime from compiling the entire tool catalog.
		if required == nil {
			required = []string{}
		}
		return TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false})
	}
	text := map[string]any{"type": "string", "maxLength": 16000}
	base := agenttools.Descriptor{Version: 1, OutputSchema: agentToolObjectOutputSchema(), Locality: agenttools.LocalityServer, Sources: agentToolboxSpaceSources, Triggers: []string{"message"}, AllowCustomAgent: true, Idempotent: true}
	list := base
	list.Name = "agents.list"
	list.Description = "List this user's personal agents in the browser workspace."
	list.Risk = serveragent.RiskRead
	list.InputSchema = schema(map[string]any{}, nil)
	list.Approval = agenttools.ApprovalNone
	manage := base
	manage.Name = "agents.configure"
	manage.Description = "Create, update, or delete a global personal agent when the user requests agent configuration. For update, provide the current version from agents.list. Browser workspace tools and model policy are automatic. Never delete the default Misty agent. Deletion requires an explicit request to delete the named agent, not just stop its task."
	manage.Risk = serveragent.RiskWrite
	manage.Approval = agenttools.ApprovalExplicitIntent
	manage.AuditEvent = "agents.configured"
	manage.InputSchema = schema(map[string]any{"operation": map[string]any{"type": "string", "enum": []string{"create", "update", "delete"}}, "agent_id": text, "version": map[string]any{"type": "integer", "minimum": 1}, "name": text, "role": text, "description": text, "instructions": text, "avatar_emoji": map[string]any{"type": "string", "maxLength": 32}, "enabled": map[string]any{"type": "boolean"}}, []string{"operation"})
	return []agenttools.Descriptor{list, manage}
}

func executeNativeAgentTool(ctx context.Context, database *db.Database, i agenttools.Invocation, r serveragent.ToolRequest) (json.RawMessage, error) {
	agents, err := database.PersonalAgents(ctx, i.UserID)
	if err != nil {
		return nil, err
	}
	if r.Name == "agents.list" {
		return json.Marshal(map[string]any{"agents": agents})
	}
	var input struct {
		Operation       string  `json:"operation"`
		AgentID         string  `json:"agent_id"`
		Version         int64   `json:"version"`
		Name            *string `json:"name"`
		Role            *string `json:"role"`
		Description     *string `json:"description"`
		Instructions    *string `json:"instructions"`
		AvatarEmoji     *string `json:"avatar_emoji"`
		Enabled         *bool   `json:"enabled"`
		ModelMode       *string `json:"model_mode"`
		ModelID         *string `json:"model_id"`
		ReasoningEffort *string `json:"reasoning_effort"`
	}
	if json.Unmarshal(r.Arguments, &input) != nil || input.Operation != "create" && input.Operation != "update" && input.Operation != "delete" {
		return nil, db.ErrSpaceInvalid
	}
	if input.Operation == "delete" {
		if err := database.DeletePersonalAgent(ctx, i.UserID, input.AgentID); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"deleted_agent_id": input.AgentID})
	}
	profile := db.AgentProfileInput{Enabled: true, ModelMode: "automatic", Icon: "sparkles"}
	if input.Operation == "update" {
		a, err := database.AskIdentityByID(ctx, i.UserID, input.AgentID)
		if err != nil {
			return nil, err
		}
		profile = db.AgentProfileInput{Name: a.Name, Role: a.Role, Description: a.Description, Instructions: a.Instructions, Icon: a.Icon, Avatar: a.Avatar, ModelMode: a.ModelMode, ModelID: a.ModelID, ReasoningEffort: a.ReasoningEffort, Enabled: a.Enabled, Version: input.Version}
	} else {
		input.AgentID = ""
	}
	if input.Name != nil {
		profile.Name = strings.TrimSpace(*input.Name)
	}
	if input.Role != nil {
		profile.Role = *input.Role
	}
	if input.Description != nil {
		profile.Description = *input.Description
	}
	if input.Instructions != nil {
		profile.Instructions = *input.Instructions
	}
	if input.AvatarEmoji != nil {
		profile.Avatar, _ = json.Marshal(map[string]string{"emoji": *input.AvatarEmoji})
	}
	if input.Enabled != nil {
		profile.Enabled = *input.Enabled
	}
	if input.ModelMode != nil {
		profile.ModelMode = *input.ModelMode
	}
	if input.ModelID != nil {
		profile.ModelID = *input.ModelID
	}
	if input.ReasoningEffort != nil {
		profile.ReasoningEffort = *input.ReasoningEffort
	}
	if profile.ModelMode == "automatic" {
		profile.ModelID = ""
		profile.ReasoningEffort = ""
	}

	saved, err := database.SavePersonalAgent(ctx, i.UserID, input.AgentID, profile)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"agent": saved})
}
