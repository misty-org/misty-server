package api

import (
	"strings"
	"unicode"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// TestingSpaceAgentSendIsGrounded requires an explicit member request before a
// companion may publish into shared chat. The generated message may be a
// concise paraphrase because the exact call remains approval-bound and audited.
func TestingSpaceAgentSendIsGrounded(prompt, message string) bool {
	rawPrompt, rawMessage := strings.TrimSpace(prompt), strings.TrimSpace(message)
	prompt, message = normalizeGroundingText(prompt), normalizeGroundingText(message)
	if prompt == "" || message == "" || len([]rune(message)) > db.MaxMessageChars || !explicitMessageSendIntent(prompt) {
		return false
	}
	promptTokens := groundingTokenSet(prompt)
	messageTokens := groundingTokenSet(message)
	if len(messageTokens) == 0 {
		return false
	}
	overlap := 0
	for token := range messageTokens {
		if promptTokens[token] {
			overlap++
		}
	}
	if explicitCitedResearchSummaryIntent(rawPrompt) && hasHTTPSourceCitation(rawMessage) && overlap > 0 {
		return true
	}
	return overlap*2 >= len(messageTokens)
}

func explicitCitedResearchSummaryIntent(prompt string) bool {
	lower := normalizeAgentIntent(strings.ToLower(prompt))
	if !strings.Contains(lower, "research") || !explicitMessageSendIntent(prompt) {
		return false
	}
	return strings.Contains(lower, "summary") || strings.Contains(lower, "findings") || strings.Contains(lower, "results")
}

func hasHTTPSourceCitation(message string) bool {
	for _, field := range strings.Fields(message) {
		candidate := strings.Trim(field, "()[]{}<>,.;:\"'")
		if strings.HasPrefix(candidate, "https://") || strings.HasPrefix(candidate, "http://") {
			return true
		}
	}
	return false
}

func groundingTokenSet(value string) map[string]bool {
	stop := map[string]bool{"a": true, "an": true, "and": true, "are": true, "at": true, "be": true, "by": true, "can": true, "everyone": true, "for": true, "i": true, "in": true, "is": true, "it": true, "me": true, "of": true, "on": true, "please": true, "the": true, "this": true, "to": true, "will": true, "you": true}
	tokens := strings.FieldsFunc(normalizeGroundingText(value), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	result := map[string]bool{}
	for _, token := range tokens {
		if len([]rune(token)) > 1 && !stop[token] {
			result[token] = true
		}
	}
	return result
}
