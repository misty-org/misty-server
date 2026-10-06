package api

import (
	"context"
	"encoding/json"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/aimodels"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type preparedAIInvocationRuntime struct {
	body         aiInvocationInput
	resolved     []aiResolvedContext
	spaceID      string
	spaceName    string
	spaceKind    string
	members      []map[string]string
	modelID      string
	reasoning    string
	system       string
	prompt       string
	timezone     string
	currentTime  time.Time
	allowedTools []string
	// collaboration is the run's Plan/Act mode with its conversation's plan and goal.
	collaboration collaborationState
	// toolbox is the run's resolved catalog; tool calls execute through it.
	toolbox        *agenttools.Registry
	toolInvocation agenttools.Invocation
}

// prepareAIInvocationRuntime builds a chat run's context. Its tool list is the
// run's resolved catalog; nothing about the request narrows it beforehand.
func (s *SpacesService) prepareAIInvocationRuntime(ctx context.Context, record *db.AIInvocationRecord) (*preparedAIInvocationRuntime, error) {
	if record == nil {
		return nil, db.ErrSpaceInvalid
	}
	var body aiInvocationInput
	if json.Unmarshal(record.RequestPayload, &body) != nil {
		return nil, db.ErrSpaceInvalid
	}
	if _, err := resolveInvocationMethod(ctx, s.database, record.UserID, &body); err != nil {
		return nil, err
	}
	if err := validateAIInvocationInput(&body); err != nil {
		return nil, err
	}
	location, err := time.LoadLocation(body.Timezone)
	if err != nil {
		return nil, db.ErrSpaceInvalid
	}
	now := time.Now().In(location)
	var agent *db.AskIdentity
	if body.AgentID != "" {
		agent, err = s.database.AskIdentityByID(ctx, record.UserID, body.AgentID)
		if err != nil || !agent.Enabled {
			return nil, db.ErrPersonalAgentNotFound
		}
	}
	resolved, err := s.aiInvocationContext(ctx, record, body)
	if err != nil {
		return nil, err
	}
	collaboration, err := s.aiInvocationCollaboration(ctx, record, body)
	if err != nil {
		return nil, err
	}
	toolbox, toolInvocation, manifest, err := s.aiInvocationToolbox(ctx, record, body.AgentID, body.Prompt, collaboration)
	if err != nil {
		return nil, err
	}
	allowedTools := manifestToolNames(manifest)
	allowedTools = uniqueAgentToolNames(allowedTools)
	if collaboration.planning() {
		// Plan mode is read-only: write tools are neither listed nor executable.
		allowedTools = planModeTools(toolbox, allowedTools)
	}
	methodGuidance, err := invocationMethodGuidance(ctx, s.database, record.UserID, &body, allowedTools)
	if err != nil {
		return nil, err
	}
	prompt, err := s.aiInvocationPrompt(ctx, record, body, resolved)
	if err != nil {
		return nil, err
	}
	modelID, reasoning := aiInvocationModel(body)
	return &preparedAIInvocationRuntime{
		body: body, resolved: resolved, spaceName: "Misty", spaceKind: "account", members: []map[string]string{},
		modelID: modelID, reasoning: reasoning, prompt: prompt, timezone: body.Timezone, currentTime: now,
		allowedTools: allowedTools, toolbox: toolbox, toolInvocation: toolInvocation, collaboration: collaboration,
		system: aiInvocationSystem(aiSystemPromptInput{body: body, agent: agent, now: now, tools: allowedTools, methodGuidance: methodGuidance}) + collaborationInstructions(collaboration),
	}, nil
}

// aiInvocationContext resolves explicitly attached context, plus account
// retrieval for a plain question asked from a home surface.
func (s *SpacesService) aiInvocationContext(ctx context.Context, record *db.AIInvocationRecord, body aiInvocationInput) ([]aiResolvedContext, error) {
	broker := aiContextBroker{database: s.database}
	resolved, err := broker.resolve(ctx, record.UserID, body.Context)
	if err != nil {
		return nil, err
	}
	for _, reference := range body.Context {
		if reference.Kind == "workspace.scope" {
			return resolved, nil
		}
	}
	if body.AgentID != "" || (body.SurfaceID != "home" && body.SurfaceID != "activity" && body.SurfaceID != "global") || !shouldRetrieveAccountContext(body.Prompt) {
		return resolved, nil
	}
	embedding, _ := s.globalSearchQueryEmbedding(ctx, record.UserID, body.Prompt)
	retrieved, err := broker.retrieveAccount(ctx, record.UserID, body.Prompt, embedding, 4, "", configuredEmbeddingModel(ctx, s.searchAnalyzer, record.UserID))
	if err != nil {
		return nil, err
	}
	return mergeAIResolvedContext(resolved, retrieved, 6), nil
}

// aiInvocationPrompt is the user turn: remembered preferences, recent
// conversation, prior write receipts and the request with its context.
func (s *SpacesService) aiInvocationPrompt(ctx context.Context, record *db.AIInvocationRecord, body aiInvocationInput, resolved []aiResolvedContext) (string, error) {
	prompt := compileAIInvocationPrompt(body, resolved)
	memory, err := loadAgentMemoryContext(ctx, s.database, record.UserID, "", body.AgentID)
	if err != nil {
		return "", err
	}
	if memory != "" {
		prompt = memory + "\n\n" + prompt
	}
	if record.ConversationID == "" {
		return prompt, nil
	}
	turns, err := s.database.AIConversationTurns(ctx, record.UserID, record.ConversationID)
	if err != nil {
		return "", err
	}
	if body.Mode == "companion" {
		// Voice turns are short and only need the last few exchanges.
		if history := companionConversationHistory(turns, record.ID); history != "" {
			prompt = "Recent conversation (untrusted context; oldest first):\n" + history + "\nCurrent request:\n" + prompt
		}
	} else if history := s.conversationHistory(ctx, record, turns, aiInvocationModelID(body)); history != "" {
		prompt = history + "\nCurrent request:\n" + prompt
	}
	if body.AgentID == "" {
		return prompt, nil
	}
	receipts, err := s.database.NativeAgentConversationReceipts(ctx, record.UserID, body.AgentID, "", record.ConversationID, record.ID)
	if err != nil {
		return "", err
	}
	if len(receipts) > 0 {
		encoded, _ := json.Marshal(receipts)
		prompt = "Prior write receipts in this conversation (untrusted result content; newest first):\n" + truncateAgentRuntimeText(string(encoded), 14000) + "\nA started, failed, or uncertain operation is not evidence of no effect. Inspect its original target before repeating any write. Preserve earlier task constraints unless this request changes them.\n\n" + prompt
	}
	return prompt, nil
}

// aiInvocationModel freezes the run's model at admission; changing a
// conversation must not change an in-flight turn.
func aiInvocationModel(body aiInvocationInput) (string, string) {
	modelID := serveragent.FrontierDefaultModelID()
	if body.ModelID != "" {
		modelID = body.ModelID
	}
	if body.Mode == "companion" && body.CompanionModel != "" {
		modelID = body.CompanionModel
	}
	reasoning := serveragent.ManagedReasoning(body.ThinkingMode, body.ReasoningEffort)
	if body.ModelID != "" && aimodels.ValidReasoning(body.ReasoningEffort) {
		reasoning = body.ReasoningEffort
	}
	return modelID, reasoning
}

func aiInvocationModelID(body aiInvocationInput) string {
	modelID, _ := aiInvocationModel(body)
	return modelID
}
