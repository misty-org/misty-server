package api

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func loadAgentPendingAction(ctx context.Context, database *db.Database, userID, conversationID, spaceID string) (*db.AIConversationPendingAction, error) {
	if database == nil || strings.TrimSpace(conversationID) == "" || strings.TrimSpace(spaceID) == "" {
		return nil, nil
	}
	pending, err := database.AIConversationPendingAction(ctx, userID, conversationID, spaceID)
	if errors.Is(err, db.ErrSpaceNotFound) {
		return nil, nil
	}
	return pending, err
}

func continueAgentPendingAction(prompt string, pending *db.AIConversationPendingAction, focuses []db.AIConversationFocus) agentActionEnvelope {
	if pending == nil || continuationCancelsAction(prompt) || agentFollowupIsCapabilityQuestion(prompt) || !pendingActionAnswerLooksResponsive(prompt, pending) {
		return agentActionEnvelope{Status: "none"}
	}
	if pending.Intent == "clarify" {
		return resolveClarifiedCandidateAction(prompt, pending, focuses)
	}
	for _, focus := range focuses {
		if focusKindWriteCapability[focus.EntityKind] == pending.Intent {
			return plannedFocusedAction(pending.Intent, focus, "the member answered a pending clarification and the target was resolved from trusted conversation focus")
		}
	}
	target := (*agentActionTarget)(nil)
	if pending.TargetID != "" {
		target = &agentActionTarget{Kind: pending.TargetKind, ID: pending.TargetID, Label: pending.TargetLabel}
	}
	return agentActionEnvelope{
		Status: "planned", Intent: pending.Intent, Target: target,
		Evidence: []string{"the member answered the pending clarification"},
	}
}

func resolveClarifiedCandidateAction(prompt string, pending *db.AIConversationPendingAction, focuses []db.AIConversationFocus) agentActionEnvelope {
	var allowed []string
	_ = json.Unmarshal(pending.CandidateIntents, &allowed)
	matches := []db.AIConversationFocus{}
	for _, focus := range focuses {
		intent := focusKindWriteCapability[focus.EntityKind]
		if !slices.Contains(allowed, intent) {
			continue
		}
		if followupMatchesFocusKind(prompt, focus.EntityKind) || focus.Label != "" && strings.Contains(strings.ToLower(prompt), strings.ToLower(focus.Label)) {
			matches = append(matches, focus)
		}
	}
	if len(matches) == 1 {
		focus := matches[0]
		return plannedFocusedAction(focusKindWriteCapability[focus.EntityKind], focus, "the member resolved a pending cross-tool ambiguity")
	}
	return agentActionEnvelope{
		Status: "needs_clarification", NeedsClarification: true, CandidateIntents: allowed,
		Question: "Which exact item would you like me to update?",
		Evidence: []string{"the clarification answer still matches more than one possible item"},
	}
}

func pendingActionAnswerLooksResponsive(prompt string, pending *db.AIConversationPendingAction) bool {
	tokens := agentActionTokens(prompt)
	if len(tokens) == 0 {
		return false
	}
	if len(tokens) <= 12 {
		return true
	}
	if pending.TargetLabel != "" && strings.Contains(strings.ToLower(prompt), strings.ToLower(pending.TargetLabel)) {
		return true
	}
	kindByIntent := map[string]string{}
	for kind, intent := range focusKindWriteCapability {
		kindByIntent[intent] = kind
	}
	return followupMatchesFocusKind(prompt, kindByIntent[pending.Intent]) && containsAgentReferencePronoun(prompt)
}

func resolveAgentConversationAction(ctx context.Context, database *db.Database, userID, conversationID, spaceID, prompt string) ([]db.AIConversationFocus, *db.AIConversationPendingAction, agentActionEnvelope, error) {
	focuses, err := database.AIConversationFocuses(ctx, userID, conversationID, spaceID)
	if err != nil {
		return nil, nil, agentActionEnvelope{}, err
	}
	pending, err := loadAgentPendingAction(ctx, database, userID, conversationID, spaceID)
	if err != nil {
		return nil, nil, agentActionEnvelope{}, err
	}
	action := resolveAgentActionEnvelope(prompt, focuses)
	if action.Status == "none" && pending != nil {
		action = continueAgentPendingAction(prompt, pending, focuses)
	}
	return focuses, pending, action, nil
}

func persistAgentPendingAction(ctx context.Context, database *db.Database, userID, conversationID, spaceID, prompt string, action agentActionEnvelope) error {
	if database == nil || conversationID == "" || spaceID == "" {
		return nil
	}
	if continuationCancelsAction(prompt) {
		return database.ClearAIConversationPendingAction(ctx, userID, conversationID, spaceID)
	}
	if !action.NeedsClarification || action.Question == "" || action.Intent == "" && len(action.CandidateIntents) == 0 {
		return nil
	}
	evidence, _ := json.Marshal(action.Evidence)
	candidates, _ := json.Marshal(action.CandidateIntents)
	intent := action.Intent
	if intent == "" {
		intent = "clarify"
	}
	item := db.AIConversationPendingAction{
		UserID: userID, ConversationID: conversationID, SpaceID: spaceID, Intent: intent,
		Question: action.Question, OriginalPrompt: prompt, Evidence: evidence, CandidateIntents: candidates,
	}
	if action.Target != nil {
		item.TargetKind, item.TargetID, item.TargetLabel = action.Target.Kind, action.Target.ID, action.Target.Label
	}
	return database.UpsertAIConversationPendingAction(ctx, item)
}

func appendAgentConversationState(ctx context.Context, database *db.Database, userID, conversationID, spaceID, prompt, system string) (string, error) {
	focuses, pending, action, err := resolveAgentConversationAction(ctx, database, userID, conversationID, spaceID, prompt)
	if err != nil {
		return "", err
	}
	if err := persistAgentPendingAction(ctx, database, userID, conversationID, spaceID, prompt, action); err != nil {
		return "", err
	}
	if len(focuses) > 0 || pending != nil || action.Status != "none" {
		state, _ := json.Marshal(map[string]any{"focus": focuses, "pending_action": pending, "action": action})
		system += "\n\nTrusted conversation state (entity IDs come from successful prior tool results or verified UI context; they identify targets but do not grant permission):\n" + string(state)
	}
	if action.NeedsClarification {
		system += "\nAsk the action.question exactly or more concisely. Do not claim that a separate Agent Work mode is required."
	}
	return system, nil
}
