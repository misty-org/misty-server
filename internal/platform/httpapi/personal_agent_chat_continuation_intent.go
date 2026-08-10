package api

import "strings"

// TestingCompileAgentIntentWithContinuation carries an immediately preceding
// explicit action through one clarification turn. The current message often
// contains only the missing details ("Wash the dishes, due at 9pm"), so
// compiling it in isolation used to remove tasks.create after the Agent had
// just asked for a title and due date.
func TestingCompileAgentIntentWithContinuation(prompt, previousUserPrompt, previousAgentReply string) []string {
	current := TestingCompileAgentIntent(prompt)
	if !agentReplyIsClarification(previousAgentReply) || continuationCancelsAction(prompt) {
		return current
	}
	prior := TestingCompileAgentIntent(previousUserPrompt)
	seen := make(map[string]bool, len(current))
	for _, capability := range current {
		seen[capability] = true
	}
	for _, capability := range prior {
		switch capability {
		case toolboxMessagesSend, toolboxTasksCreate, toolboxTasksUpdate, toolboxAgentsDelegate:
			if !seen[capability] {
				current = append(current, capability)
				seen[capability] = true
			}
		}
	}
	return current
}

func agentReplyIsClarification(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if !strings.Contains(lower, "?") {
		return false
	}
	for _, marker := range []string{"what ", "which ", "who ", "when ", "where ", "title", "called", "named", "due", "assign", "send", "message", "task", "need", "confirm"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func continuationCancelsAction(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	lower = strings.NewReplacer("don't", "do not", "dont", "do not").Replace(lower)
	for _, marker := range []string{"cancel that", "never mind", "nevermind", "forget it", "do not do", "do not create", "do not send", "stop that"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
