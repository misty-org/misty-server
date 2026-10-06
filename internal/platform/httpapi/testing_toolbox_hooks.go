package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Test hooks: run one Space or chat tool through the same toolbox resolution
// and execution path production uses.

func TestingExecuteSpaceConversationTaskTool(ctx context.Context, database *db.Database, userID, spaceID, agentID, prompt, name string, arguments json.RawMessage) (json.RawMessage, error) {
	return TestingExecuteSpaceConversationTool(ctx, database, userID, spaceID, agentID, prompt, name, arguments)
}

func TestingExecuteSpaceConversationTool(ctx context.Context, database *db.Database, userID, spaceID, agentID, prompt, name string, arguments json.RawMessage) (json.RawMessage, error) {
	toolbox := spaceAgentToolbox(database)
	invocation := agenttools.Invocation{
		UserID: userID, SpaceID: spaceID, AgentID: agentID, Source: "space_conversation", Trigger: "message", OriginalInput: prompt,
		SessionID: "testing:" + userID + ":" + spaceID,
	}
	digest := sha256.Sum256([]byte(prompt + "\x00" + name + "\x00" + string(arguments)))
	return executeSpaceAgentToolbox(ctx, toolbox, invocation, database, serveragent.ToolRequest{ID: "test-" + hex.EncodeToString(digest[:]), Name: name, Arguments: arguments})
}

func TestingResolveAIInvocationSpaceToolNames(ctx context.Context, database *db.Database, userID, spaceID, invocationID, prompt string) ([]string, error) {
	if err := TestingAdmitToolInvocation(ctx, database, userID, spaceID, invocationID, prompt); err != nil {
		return nil, err
	}
	_, _, manifest, err := resolveAIInvocationSpaceToolbox(ctx, database, spaceConversationToolActor{
		userID: userID, spaceID: spaceID, runID: invocationID,
	}, prompt)
	return manifestToolNames(manifest), err
}

func TestingResolveAIInvocationSpaceToolNamesWithConversation(ctx context.Context, database *db.Database, userID, spaceID, conversationID, invocationID, prompt string) ([]string, error) {
	if err := TestingAdmitToolInvocation(ctx, database, userID, spaceID, invocationID, prompt); err != nil {
		return nil, err
	}
	_, _, manifest, err := resolveAIInvocationSpaceToolbox(ctx, database, spaceConversationToolActor{
		userID: userID, spaceID: spaceID, runID: invocationID, sessionID: conversationID,
	}, prompt)
	return manifestToolNames(manifest), err
}

func TestingExecuteAIInvocationSpaceTool(ctx context.Context, database *db.Database, userID, spaceID, invocationID, prompt, name string, arguments json.RawMessage) (json.RawMessage, error) {
	return TestingExecuteAIInvocationSpaceToolWithConversation(ctx, database, userID, spaceID, "", invocationID, prompt, name, arguments)
}

func TestingExecuteAIInvocationSpaceToolWithConversation(ctx context.Context, database *db.Database, userID, spaceID, conversationID, invocationID, prompt, name string, arguments json.RawMessage) (json.RawMessage, error) {
	if err := TestingAdmitToolInvocation(ctx, database, userID, spaceID, invocationID, prompt); err != nil {
		return nil, err
	}
	toolbox, invocation, manifest, err := resolveAIInvocationSpaceToolbox(ctx, database, spaceConversationToolActor{
		userID: userID, spaceID: spaceID, runID: invocationID, sessionID: conversationID,
	}, prompt)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(manifest.Tools, func(tool serveragent.ToolDefinition) bool { return tool.Name == name }) {
		return nil, agenttools.ErrCapabilityDenied
	}
	return executeSpaceAgentToolbox(ctx, toolbox, invocation, database, serveragent.ToolRequest{
		ID: "testing-" + name, Name: name, Arguments: arguments,
	})
}

func TestingAdmitToolInvocation(ctx context.Context, database *db.Database, userID, spaceID, id, prompt string) error {
	if database == nil || id == "" {
		return nil
	}
	if _, err := database.AIInvocationByID(ctx, userID, id); err == nil {
		return nil
	} else if !errors.Is(err, db.ErrSpaceNotFound) {
		return err
	}
	_, _, err := database.CreateAIInvocationRecord(ctx, db.AIInvocationRecord{ID: id, UserID: userID, SpaceID: spaceID, SurfaceID: "notes", Mode: "quick", Trigger: "selection", State: "running", IdempotencyKey: id, RequestPayload: TestingMustAPIRawJSON(map[string]any{"prompt": prompt}), ExpiresAt: time.Now().Add(time.Hour)})
	return err
}
