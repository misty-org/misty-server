package api

import (
	"context"
	"encoding/json"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func previousAIConversationExchange(turns []db.AIConversationTurnRecord, currentInvocationID string) (string, string) {
	for index := len(turns) - 1; index >= 0; index-- {
		turn := turns[index]
		if turn.InvocationID == currentInvocationID || strings.TrimSpace(turn.Prompt) == "" {
			continue
		}
		reply := firstAIText(turn.Reply, turn.Failure, turn.AgentError, turn.Status)
		return strings.TrimSpace(turn.Prompt), strings.TrimSpace(reply)
	}
	return "", ""
}

func aiInvocationRequestedSpaceTools(prompt, previousUserPrompt, previousAgentReply string) []string {
	requested := []string{
		toolboxContextGet, toolboxMembersList, toolboxMembersResolve,
		toolboxMessagesSearch, toolboxLibrarySearch, toolboxLibraryRead,
		toolboxTasksQuery, "calendar.query", toolboxNotesSearch, toolboxNotesRead,
		toolboxDrawingsList, toolboxDrawingsRead, toolboxRoadmapsQuery, toolboxRoadmapsRead,
	}
	requested = append(requested, TestingCompileAgentIntentWithContinuation(prompt, previousUserPrompt, previousAgentReply)...)
	return uniqueAgentToolNames(requested)
}

func TestingAIInvocationRequestedSpaceTools(prompt, previousUserPrompt, previousAgentReply string) []string {
	return aiInvocationRequestedSpaceTools(prompt, previousUserPrompt, previousAgentReply)
}

func TestingResolveAIInvocationSpaceToolNames(ctx context.Context, database *db.Database, userID, spaceID, invocationID, prompt string) ([]string, error) {
	_, _, manifest, err := resolveAIInvocationSpaceToolbox(ctx, database, spaceConversationToolActor{
		userID: userID, spaceID: spaceID, runID: invocationID,
	}, prompt, "", "")
	return manifestToolNames(manifest), err
}

func TestingResolveAIInvocationSpaceToolNamesWithConversation(ctx context.Context, database *db.Database, userID, spaceID, conversationID, invocationID, prompt string) ([]string, error) {
	_, _, manifest, err := resolveAIInvocationSpaceToolbox(ctx, database, spaceConversationToolActor{
		userID: userID, spaceID: spaceID, runID: invocationID, sessionID: conversationID,
	}, prompt, "", "")
	return manifestToolNames(manifest), err
}

func TestingExecuteAIInvocationSpaceTool(ctx context.Context, database *db.Database, userID, spaceID, invocationID, prompt, name string, arguments json.RawMessage) (json.RawMessage, error) {
	return TestingExecuteAIInvocationSpaceToolWithConversation(ctx, database, userID, spaceID, "", invocationID, prompt, name, arguments)
}

func TestingExecuteAIInvocationSpaceToolWithConversation(ctx context.Context, database *db.Database, userID, spaceID, conversationID, invocationID, prompt, name string, arguments json.RawMessage) (json.RawMessage, error) {
	toolbox, invocation, manifest, err := resolveAIInvocationSpaceToolbox(ctx, database, spaceConversationToolActor{
		userID: userID, spaceID: spaceID, runID: invocationID, sessionID: conversationID,
	}, prompt, "", "")
	if err != nil {
		return nil, err
	}
	if !agentManifestHasTool(manifest, name) {
		return nil, agenttools.ErrCapabilityDenied
	}
	return executeSpaceAgentToolbox(ctx, toolbox, invocation, database, serveragent.ToolRequest{
		ID: "testing-" + name, Name: name, Arguments: arguments,
	})
}

func resolveAIInvocationSpaceToolbox(ctx context.Context, database *db.Database, actor spaceConversationToolActor, prompt, previousUserPrompt, previousAgentReply string) (*agenttools.Registry, agenttools.Invocation, serveragent.ToolManifest, error) {
	requested := aiInvocationRequestedSpaceTools(prompt, previousUserPrompt, previousAgentReply)
	if database != nil && strings.TrimSpace(actor.sessionID) != "" {
		_, _, action, actionErr := resolveAgentConversationAction(ctx, database, actor.userID, actor.sessionID, actor.spaceID, prompt)
		if actionErr != nil {
			return nil, agenttools.Invocation{}, serveragent.ToolManifest{}, actionErr
		}
		if action.Status == "planned" && action.Intent != "" {
			requested = append(requested, action.Intent)
		}
	}
	browserTabs, browserCapabilities := aiInvocationBrowserGrants(ctx, database, actor.userID, actor.runID)
	toolbox := spaceAgentToolboxWithBrowser(database, browserTabs, browserCapabilities)
	for _, descriptor := range browserToolDescriptors() {
		if browserCapabilities[descriptor.Name] {
			requested = append(requested, descriptor.Name)
		}
	}
	requested = uniqueAgentToolNames(requested)
	explicit := make(map[string]bool, len(requested))
	for _, name := range requested {
		explicit[name] = true
	}
	invocation := agenttools.Invocation{
		UserID: actor.userID, SpaceID: actor.spaceID, AgentID: actor.agentID,
		RunID: actor.runID, SessionID: actor.sessionID, Source: "space_conversation",
		Trigger: "message", OriginalInput: prompt, ExplicitTools: explicit,
		ConversationScopeKind: db.ConversationScopeEveryone,
	}
	manifest, err := toolbox.Resolve(ctx, invocation, requested, authorizeSpaceAgentTool(database))
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

func agentManifestHasTool(manifest serveragent.ToolManifest, name string) bool {
	for _, tool := range manifest.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}
