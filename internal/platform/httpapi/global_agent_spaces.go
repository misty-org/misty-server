package api

import (
	"context"
	"encoding/json"
	"strings"

	agent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type globalSpaceCallKey struct{}

type globalSpaceCall struct {
	spaceID   string
	sessionID string
}

// Global agents discover destinations through conversation. Each operation is
// still authorized against the account and the actual destination at execution.
func globalAgentSpaceDescriptors() []agenttools.Descriptor {
	base := agenttools.Descriptor{Version: 1, Risk: agent.RiskRead, Approval: agenttools.ApprovalNone, OutputSchema: agentToolObjectOutputSchema(), Locality: agenttools.LocalityServer, Sources: agentToolboxSpaceSources, Triggers: []string{"message"}, AllowCustomAgent: true, Idempotent: true}
	schema := func(properties map[string]any, required []string) json.RawMessage {
		return TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false})
	}
	space := map[string]any{"type": "string", "minLength": 1}
	list := base
	list.Name = "spaces.list"
	list.Description = "List spaces this user can access. Resolve destinations from the user's words. If names are ambiguous, ask; never require a context picker."
	list.InputSchema = schema(map[string]any{}, []string{})
	catalog := base
	catalog.Name = "spaces.tools"
	catalog.Description = "Discover available app tools and their input schemas in a named destination from spaces.list. Use spaces.execute for operations in that space."
	catalog.InputSchema = schema(map[string]any{"space_id": space}, []string{"space_id"})
	execute := base
	execute.Name = "spaces.execute"
	execute.Description = "Run a tool returned by spaces.tools in the requested space. Supply its exact name and arguments. The destination and operation must follow the user's intent. Never guess an ambiguous destination."
	execute.Risk = agent.RiskWrite
	execute.Approval = agenttools.ApprovalExplicitIntent
	execute.AuditEvent = "space.tool.executed"
	execute.InputSchema = schema(map[string]any{"space_id": space, "tool": space, "arguments": map[string]any{"type": "object", "additionalProperties": true}}, []string{"space_id", "tool", "arguments"})
	return []agenttools.Descriptor{list, catalog, execute}
}

func globalSpaceTool(name string) bool {
	// Scope routing is only for built-in Space data. Device, provider, agent setup
	// and nested routing tools retain their own account/runtime boundaries.
	switch strings.Split(name, ".")[0] {
	case "context", "members", "messages", "library", "tasks", "calendar", "notes", "drawings", "roadmaps", "roadmap":
		return true
	}
	return false
}

func executeGlobalAgentSpaceTool(ctx context.Context, database *db.Database, invocation agenttools.Invocation, request agent.ToolRequest) (json.RawMessage, error) {
	if invocation.AgentID == "" || db.AppAuthorityFromContext(ctx) != nil {
		return nil, db.ErrSpaceForbidden
	}
	if request.Name == "spaces.list" {
		spaces, err := database.ListSpaces(ctx, invocation.UserID)
		if err != nil {
			return nil, err
		}
		items := []map[string]string{}
		for _, space := range spaces {
			items = append(items, map[string]string{"id": space.ID, "name": space.Name})
		}
		return json.Marshal(map[string]any{"spaces": items, "current_space_id": invocation.SpaceID})
	}
	var input struct {
		SpaceID   string          `json:"space_id"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(request.Arguments, &input) != nil || strings.TrimSpace(input.SpaceID) == "" {
		return nil, db.ErrSpaceInvalid
	}
	if _, err := database.SpaceByID(ctx, invocation.UserID, input.SpaceID); err != nil {
		return nil, err
	}
	actor := spaceConversationToolActor{userID: invocation.UserID, spaceID: input.SpaceID, agentID: invocation.AgentID, runID: invocation.RunID}
	if input.SpaceID == invocation.SpaceID {
		actor.sessionID = invocation.SessionID
	}
	toolbox, target, manifest, err := resolveAIInvocationSpaceToolbox(ctx, database, actor, invocation.OriginalInput, "", "")
	if err != nil {
		return nil, err
	}
	if request.Name == "spaces.tools" {
		tools := []agent.ToolDefinition{}
		for _, tool := range manifest.Tools {
			if globalSpaceTool(tool.Name) {
				tools = append(tools, tool)
			}
		}
		return json.Marshal(map[string]any{"space_id": input.SpaceID, "tools": tools})
	}
	if !globalSpaceTool(input.Tool) || !agentManifestHasTool(manifest, input.Tool) || len(input.Arguments) == 0 {
		return nil, db.ErrSpaceForbidden
	}
	call := globalSpaceCall{spaceID: input.SpaceID, sessionID: invocation.SessionID}
	return executeSpaceAgentToolbox(context.WithValue(ctx, globalSpaceCallKey{}, call), toolbox, target, database, agent.ToolRequest{ID: request.ID, Name: input.Tool, Arguments: input.Arguments})
}
