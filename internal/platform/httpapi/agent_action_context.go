package api

import (
	"context"
	"encoding/json"
	"strings"
	"unicode"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type agentActionTarget struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

type agentActionEnvelope struct {
	Status             string             `json:"status"`
	Intent             string             `json:"intent,omitempty"`
	Target             *agentActionTarget `json:"target,omitempty"`
	Evidence           []string           `json:"evidence,omitempty"`
	CandidateIntents   []string           `json:"candidate_intents,omitempty"`
	NeedsClarification bool               `json:"needs_clarification,omitempty"`
	Question           string             `json:"question,omitempty"`
}

var focusKindWriteCapability = map[string]string{
	"task":           toolboxTasksUpdate,
	"note":           toolboxNotesUpdate,
	"drawing":        toolboxDrawingsApply,
	"calendar_event": toolboxCalendarUpdate,
	"roadmap":        toolboxRoadmapsUpdate,
	"library_item":   toolboxLibraryUpdate,
}

func resolveAgentActionEnvelope(prompt string, focuses []db.AIConversationFocus) agentActionEnvelope {
	if continuationCancelsAction(prompt) || agentFollowupIsCapabilityQuestion(prompt) {
		return agentActionEnvelope{Status: "none"}
	}
	current := TestingCompileAgentIntent(prompt)
	for _, capability := range current {
		for _, focus := range focuses {
			if focusKindWriteCapability[focus.EntityKind] == capability {
				return plannedFocusedAction(capability, focus, "explicit resource and action in the current turn")
			}
		}
	}
	if !referentialMutationFollowup(prompt) {
		return agentActionEnvelope{Status: "none"}
	}
	candidates := []db.AIConversationFocus{}
	for _, focus := range focuses {
		capability := focusKindWriteCapability[focus.EntityKind]
		if capability != "" && followupMatchesFocusKind(prompt, focus.EntityKind) {
			candidates = append(candidates, focus)
		}
	}
	if len(candidates) == 0 {
		mutable := []db.AIConversationFocus{}
		for _, focus := range focuses {
			if focusKindWriteCapability[focus.EntityKind] != "" {
				mutable = append(mutable, focus)
			}
		}
		if len(mutable) == 1 {
			candidates = mutable
		}
	}
	if len(candidates) == 1 {
		focus := candidates[0]
		return plannedFocusedAction(focusKindWriteCapability[focus.EntityKind], focus, "referential follow-up resolved from a successful prior tool result")
	}
	if len(candidates) > 1 || containsAgentReferencePronoun(prompt) {
		intent, kind := inferReferentialMutationCapability(prompt)
		candidateIntents := []string{}
		for _, focus := range candidates {
			candidateIntents = append(candidateIntents, focusKindWriteCapability[focus.EntityKind])
		}
		candidateIntents = uniqueAgentToolNames(candidateIntents)
		return agentActionEnvelope{
			Status: "needs_clarification", Intent: intent, NeedsClarification: true,
			Question: clarificationQuestionForCandidates(kind, candidates), CandidateIntents: candidateIntents,
			Evidence: []string{"the current turn refers to an earlier item but does not identify one unambiguously"},
		}
	}
	return agentActionEnvelope{Status: "none"}
}

func clarificationQuestionForCandidates(kind string, candidates []db.AIConversationFocus) string {
	if len(candidates) > 1 {
		labels := make([]string, 0, min(3, len(candidates)))
		for _, item := range candidates {
			label := strings.TrimSpace(item.Label)
			if label == "" {
				label = strings.ReplaceAll(item.EntityKind, "_", " ")
			}
			labels = append(labels, label)
			if len(labels) == 3 {
				break
			}
		}
		return "Which item do you mean: " + strings.Join(labels, " or ") + "?"
	}
	return clarificationQuestionForKind(kind)
}

func inferReferentialMutationCapability(prompt string) (string, string) {
	kinds := []string{"task", "note", "drawing", "calendar_event", "roadmap", "library_item"}
	matchedKind := ""
	for _, kind := range kinds {
		if !followupMatchesFocusKind(prompt, kind) {
			continue
		}
		if matchedKind != "" {
			return "", ""
		}
		matchedKind = kind
	}
	return focusKindWriteCapability[matchedKind], matchedKind
}

func clarificationQuestionForKind(kind string) string {
	labels := map[string]string{
		"task": "task", "note": "note", "drawing": "drawing", "calendar_event": "calendar event",
		"roadmap": "roadmap", "library_item": "Library item",
	}
	if label := labels[kind]; label != "" {
		return "Which " + label + " would you like me to update?"
	}
	return "Which item would you like me to update?"
}

func plannedFocusedAction(capability string, focus db.AIConversationFocus, evidence string) agentActionEnvelope {
	return agentActionEnvelope{
		Status: "planned", Intent: capability,
		Target:   &agentActionTarget{Kind: focus.EntityKind, ID: focus.EntityID, Label: focus.Label},
		Evidence: []string{evidence},
	}
}

func referentialMutationFollowup(prompt string) bool {
	if !containsAgentReferencePronoun(prompt) {
		return false
	}
	tokens := agentActionTokens(prompt)
	for index, token := range tokens {
		switch token {
		case "add", "append", "assign", "change", "complete", "edit", "make", "mark", "move", "reassign", "rename", "replace", "reschedule", "set", "update":
			if !agentIntentTokenNegated(tokens, index) {
				return true
			}
		}
	}
	return false
}

func containsAgentReferencePronoun(prompt string) bool {
	tokens := agentActionTokens(prompt)
	for _, token := range tokens {
		if token == "it" || token == "that" || token == "this" || token == "one" || token == "instead" {
			return true
		}
	}
	return false
}

func followupMatchesFocusKind(prompt, kind string) bool {
	value := " " + strings.Join(agentActionTokens(prompt), " ") + " "
	phrases := map[string][]string{
		"task":           {" task ", " chore ", " todo ", " assign ", " assignee ", " priority ", " due ", " complete ", " done ", " description ", " notes "},
		"note":           {" note ", " journal ", " markdown ", " paragraph ", " append "},
		"drawing":        {" drawing ", " canvas ", " diagram ", " shape ", " color "},
		"calendar_event": {" calendar ", " event ", " meeting ", " appointment ", " reschedule "},
		"roadmap":        {" roadmap ", " milestone ", " goal ", " plan "},
		"library_item":   {" library ", " file ", " photo ", " video ", " document ", " attachment ", " caption ", " favorite "},
	}
	for _, phrase := range phrases[kind] {
		if strings.Contains(value, phrase) {
			return true
		}
	}
	return false
}

func agentActionTokens(value string) []string {
	return strings.FieldsFunc(normalizeAgentIntent(strings.ToLower(value)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-'
	})
}

func agentFollowupIsCapabilityQuestion(prompt string) bool {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	for _, prefix := range []string{"how do ", "how can ", "how would ", "what can ", "what happens ", "why "} {
		if strings.HasPrefix(lower, prefix) && strings.Contains(lower, "?") {
			return true
		}
	}
	return false
}

func agentActionEnvelopeJSON(envelope agentActionEnvelope) string {
	if envelope.Status == "none" || envelope.Status == "" {
		return ""
	}
	raw, _ := json.Marshal(envelope)
	return string(raw)
}

func TestingResolveAgentActionEnvelope(prompt string, focuses []db.AIConversationFocus) json.RawMessage {
	raw, _ := json.Marshal(resolveAgentActionEnvelope(prompt, focuses))
	return raw
}

func requireAgentMutationTarget(ctx context.Context, database *db.Database, actor spaceConversationToolActor, prompt, kind, id string, aliases ...string) error {
	lowerPrompt := strings.ToLower(prompt)
	for _, candidate := range append([]string{id}, aliases...) {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate != "" && strings.Contains(lowerPrompt, candidate) {
			return nil
		}
	}
	if actor.sessionID != "" {
		focus, err := database.AIConversationFocusByKind(ctx, actor.userID, actor.sessionID, actor.spaceID, kind)
		if err == nil && focus.EntityID == id {
			return nil
		}
	}
	return serveragent.ErrInvalidRequest("target_not_grounded: search or read the exact item first, then retry with the returned id")
}
