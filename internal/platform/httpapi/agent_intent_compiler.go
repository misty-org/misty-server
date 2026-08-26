package api

import (
	"strings"
	"unicode"
)

func explicitMessageSendIntent(prompt string) bool {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	for _, prefix := range []string{"how do ", "how can ", "how would ", "what does ", "what happens ", "why ", "whether ", "can agents ", "can an agent ", "do agents ", "are agents "} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	lower = strings.NewReplacer("don't", "do not", "dont", "do not", "can't", "cannot", "cant", "cannot").Replace(lower)
	tokens := strings.FieldsFunc(lower, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' })
	for index, token := range tokens {
		if token == "tell" && index+1 < len(tokens) && (tokens[index+1] == "me" || tokens[index+1] == "us") {
			continue
		}
		action := token == "send" || token == "text" || token == "tell" || token == "post" || token == "message" || token == "notify"
		if token == "let" {
			for next := index + 1; next < len(tokens) && next <= index+4; next++ {
				if tokens[next] == "know" {
					action = true
				}
			}
		}
		if action && !agentIntentTokenNegated(tokens, index) {
			return true
		}
	}
	return false
}

func TestingCompileAgentIntent(prompt string) []string {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	allowed := []string{"tasks.query"}
	if explicitResourceMention(lower, []string{"draw", "drawing", "drawings", "sketch", "illustration", "canvas", "excalidraw", "diagram", "flowchart", "whiteboard"}) {
		allowed = append(allowed, toolboxDrawingsList, toolboxDrawingsRead)
	}
	if explicitMessageSendIntent(prompt) {
		allowed = append(allowed, toolboxMessagesSend)
	}
	if explicitTaskWriteIntent(lower, "create", "add", "make", "open") {
		allowed = append(allowed, "tasks.create")
	}
	if explicitTaskWriteIntent(lower, "update", "change", "mark", "set", "assign", "complete") {
		allowed = append(allowed, "tasks.update")
	}
	if explicitAgentDelegationIntent(lower) {
		allowed = append(allowed, toolboxAgentsDelegate)
	}
	if explicitMistyMemoryIntent(lower, toolboxMemoryRemember) {
		allowed = append(allowed, toolboxMemoryRemember)
	}
	if explicitMistyMemoryIntent(lower, toolboxMemoryForget) {
		allowed = append(allowed, toolboxMemoryForget)
	}
	if explicitResourceWriteIntent(lower, []string{"note", "notes", "journal"}, []string{"create", "add", "make", "write", "draft"}) {
		allowed = append(allowed, toolboxNotesCreate)
	}
	if explicitResearchPersistenceIntent(lower) {
		allowed = append(allowed, toolboxNotesCreate)
	}
	if explicitResourceWriteIntent(lower, []string{"note", "notes", "journal"}, []string{"update", "edit", "change", "append", "revise"}) {
		allowed = append(allowed, toolboxNotesUpdate)
	}
	if explicitResourceWriteIntent(lower, []string{"draw", "drawing", "drawings", "sketch", "illustration", "canvas", "excalidraw", "diagram", "flowchart", "whiteboard"}, []string{"create", "add", "make", "draw", "sketch", "illustrate", "build"}) {
		allowed = append(allowed, toolboxDrawingsCreate, toolboxDrawingsApply)
	}
	if explicitResourceWriteIntent(lower, []string{"draw", "drawing", "drawings", "sketch", "illustration", "canvas", "excalidraw", "diagram", "flowchart", "whiteboard"}, []string{"update", "edit", "change", "move", "arrange", "replace", "delete", "clear"}) {
		allowed = append(allowed, toolboxDrawingsApply)
	}
	if explicitResourceWriteIntent(lower, []string{"calendar", "event", "meeting", "appointment"}, []string{"create", "add", "make", "schedule", "book"}) {
		allowed = append(allowed, toolboxCalendarCreate)
	}
	if explicitResourceWriteIntent(lower, []string{"calendar", "event", "meeting", "appointment"}, []string{"update", "edit", "change", "move", "reschedule", "cancel"}) {
		allowed = append(allowed, toolboxCalendarUpdate)
	}
	if explicitResourceWriteIntent(lower, []string{"roadmap", "roadmaps", "plan"}, []string{"create", "add", "make", "draft"}) {
		allowed = append(allowed, toolboxRoadmapsCreate)
	}
	if explicitResourceWriteIntent(lower, []string{"roadmap", "roadmaps", "plan"}, []string{"update", "edit", "change", "revise", "rename"}) {
		allowed = append(allowed, toolboxRoadmapsUpdate)
	}
	if explicitResourceWriteIntent(lower, []string{"library", "file", "photo", "video", "document", "attachment"}, []string{"update", "edit", "change", "rename", "tag", "caption", "favorite", "hide"}) {
		allowed = append(allowed, toolboxLibraryUpdate)
	}
	if explicitResourceWriteIntent(lower, []string{"library", "attachment"}, []string{"save", "add", "promote", "keep"}) {
		allowed = append(allowed, toolboxLibraryPromoteAttachment)
	}
	return allowed
}

func explicitResearchPersistenceIntent(value string) bool {
	value = normalizeAgentIntent(value)
	if !strings.Contains(value, "research") {
		return false
	}
	return explicitResourceWriteIntent(value, []string{"research"}, []string{"save", "keep", "store", "record", "capture"})
}

func explicitResourceMention(value string, resources []string) bool {
	tokens := strings.FieldsFunc(normalizeAgentIntent(value), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for _, token := range tokens {
		for _, resource := range resources {
			if token == resource {
				return true
			}
		}
	}
	return false
}

func explicitResourceWriteIntent(value string, resources, actions []string) bool {
	value = normalizeAgentIntent(value)
	tokens := strings.FieldsFunc(value, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	resourceFound := false
	for _, token := range tokens {
		for _, resource := range resources {
			resourceFound = resourceFound || token == resource
		}
	}
	if !resourceFound {
		return false
	}
	for index, token := range tokens {
		for _, action := range actions {
			if token == action && !agentIntentTokenNegated(tokens, index) {
				return true
			}
		}
	}
	return false
}

func explicitAgentDelegationIntent(value string) bool {
	value = normalizeAgentIntent(value)
	for _, denial := range []string{"do not delegate", "do not ask", "do not send", "cannot delegate"} {
		if strings.Contains(value, denial) {
			return false
		}
	}
	if genericAgentDelegationQuestion(value) {
		return false
	}
	for _, phrase := range []string{"delegate ", "hand this to ", "route this to ", "send this to ", "ask the agent ", "ask agent ", "ask my agent ", "ask our agent ", "ask the teammate ", "ask my teammate ", "ask our teammate "} {
		if strings.Contains(value, phrase) {
			return true
		}
	}
	tokens := strings.FieldsFunc(value, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for index, token := range tokens {
		if token == "ask" {
			for _, later := range tokens[index+1:] {
				if later == "to" {
					return true
				}
			}
		}
	}
	return false
}

func genericAgentDelegationQuestion(value string) bool {
	for _, phrase := range []string{"can you delegate", "are you able to delegate", "can you route work", "what agents can", "what can agents"} {
		if strings.Contains(value, phrase) {
			return true
		}
	}
	return false
}

func explicitTaskWriteIntent(value string, words ...string) bool {
	value = normalizeAgentIntent(value)
	if genericTaskCapabilityQuestion(value) {
		return false
	}
	tokens := strings.FieldsFunc(value, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' })
	hasTask := false
	for _, token := range tokens {
		hasTask = hasTask || token == "task" || token == "tasks" || token == "planner" || token == "todo" || token == "chore" || token == "chores"
	}
	if !hasTask {
		return false
	}
	for index, token := range tokens {
		for _, word := range words {
			if token == word && !agentIntentTokenNegated(tokens, index) {
				return true
			}
		}
	}
	return false
}

func genericTaskCapabilityQuestion(value string) bool {
	capabilityQuestion := false
	for _, phrase := range []string{"what can you", "what are you able", "are you able", "do you support", "is it possible"} {
		capabilityQuestion = capabilityQuestion || strings.Contains(value, phrase)
	}
	if !capabilityQuestion {
		return false
	}
	for _, marker := range []string{" task called ", " task named ", " task titled ", " task to ", " task for "} {
		if strings.Contains(value, marker) {
			return false
		}
	}
	return true
}

func normalizeAgentIntent(value string) string {
	return strings.NewReplacer("don't", "do not", "dont", "do not", "can't", "cannot", "cant", "cannot").Replace(value)
}

func agentIntentTokenNegated(tokens []string, index int) bool {
	for previous := max(0, index-3); previous < index; previous++ {
		if tokens[previous] == "not" || tokens[previous] == "never" || tokens[previous] == "without" || tokens[previous] == "cannot" {
			return true
		}
	}
	return false
}
