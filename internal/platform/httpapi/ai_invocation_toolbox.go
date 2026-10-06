package api

import (
	"context"
	"encoding/json"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// aiInvocationToolbox resolves one chat run's complete catalog: Misty data
// tools, attached browser tools and the agent's connected tools. The runtime
// context, the MCP server and every tool call use this same resolution.
func (s *SpacesService) aiInvocationToolbox(ctx context.Context, record *db.AIInvocationRecord, agentID, prompt string, collaboration collaborationState) (*agenttools.Registry, agenttools.Invocation, serveragent.ToolManifest, error) {
	actor := spaceConversationToolActor{userID: record.UserID, agentID: agentID, runID: record.ID, sessionID: record.ConversationID}
	connected := append(s.aiInvocationConnectedTools(record), s.deviceToolRegistrations(ctx, record)...)
	connected = append(connected, s.collaborationToolRegistrations(record, collaboration)...)
	return resolveAIInvocationSpaceToolbox(ctx, s.database, actor, prompt, connected...)
}

// aiInvocationConnectedTools are the user's connected apps, the screen tools
// on the desktop, and account administration: workflow schedules and Spaces.
func (s *SpacesService) aiInvocationConnectedTools(record *db.AIInvocationRecord) []agenttools.Registration {
	tools := append(s.appsToolRegistrations(), s.screenToolRegistrations(record)...)
	return append(tools, s.accountAdminToolRegistrations()...)
}

func resolveAIInvocationSpaceToolbox(ctx context.Context, database *db.Database, actor spaceConversationToolActor, prompt string, connected ...agenttools.Registration) (*agenttools.Registry, agenttools.Invocation, serveragent.ToolManifest, error) {
	browserTabs, browserCapabilities := aiInvocationBrowserGrants(ctx, database, actor.userID, actor.runID)
	options := agentToolboxOptions{
		accountLevel: actor.spaceID == "", browserTabs: browserTabs, browserCapabilities: browserCapabilities,
		extra: append(connected, agentMemberToolRegistrations(database)...),
	}
	if actor.agentID == "" {
		options.delegation = func(ctx context.Context, invocation agenttools.Invocation, request serveragent.ToolRequest) (json.RawMessage, error) {
			var input struct {
				Prompt string `json:"prompt"`
			}
			if json.Unmarshal(request.Arguments, &input) != nil || strings.TrimSpace(input.Prompt) == "" {
				return nil, db.ErrSpaceInvalid
			}
			identity, err := database.EnsureAskIdentity(ctx, actor.userID, serveragent.InitialSelectedModelID)
			if err != nil {
				return nil, err
			}
			child, err := database.CreateCreatorAgentRun(ctx, actor.userID, actor.spaceID, identity.ID, db.CreatorAgentRunInput{Instruction: input.Prompt, Mode: "auto", ParentInvocationID: actor.runID, AIConversationID: actor.sessionID})
			if err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"run_id": child.ID, "state": child.State, "worker": "background"})
		}
	}
	toolbox := buildAgentToolbox(database, options)
	names := make([]string, 0, len(toolbox.Descriptors()))
	for _, descriptor := range toolbox.Descriptors() {
		names = append(names, descriptor.Name)
	}
	invocation := agenttools.Invocation{
		UserID: actor.userID, SpaceID: actor.spaceID, AgentID: actor.agentID,
		RunID: actor.runID, SessionID: actor.sessionID, Source: "space_conversation",
		Trigger: "message", OriginalInput: prompt, ConversationScopeKind: db.ConversationScopeEveryone,
	}
	manifest, err := toolbox.Resolve(ctx, invocation, names, authorizeSpaceAgentTool(database))
	return toolbox, invocation, manifest, err
}

func aiInvocationBrowserGrants(ctx context.Context, database *db.Database, userID, invocationID string) ([]string, map[string]bool) {
	labels := []string{}
	capabilities := map[string]bool{}
	if database == nil || !isAIInvocationRuntimeID(invocationID) {
		return labels, capabilities
	}
	contexts, err := database.AIInvocationContexts(ctx, userID, invocationID)
	if err != nil {
		return labels, capabilities
	}
	for _, item := range contexts {
		var granted []string
		if json.Unmarshal(item.Capabilities, &granted) != nil {
			continue
		}
		for _, capability := range granted {
			capabilities[capability] = true
		}
		label := strings.TrimSpace(item.DisplayName)
		if label == "" {
			label = "Misty browser"
		}
		labels = append(labels, label+" (scopeId "+item.OpaqueRef+")")
	}
	if capabilities["browser.inspect"] {
		capabilities["browser.request_user_action"] = true
	}
	return labels, capabilities
}

func agentToolNameAllowed(allowed []string, name string) bool {
	for _, candidate := range allowed {
		if candidate == name {
			return true
		}
	}
	return false
}

