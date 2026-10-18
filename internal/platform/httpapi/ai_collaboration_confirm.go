package api

import (
	"regexp"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Asking the person to approve what they already asked for ("Confirm post",
// "Yes, post it / Revise first") only stalls the run: the request is the
// approval, and Misty shows its own approval card where the person's settings
// call for one. Such questions are refused, and the agent is told to go on.
var (
	confirmHeader = regexp.MustCompile(`(?i)^(confirm|approve|approval|proceed|go ahead|ok to|okay to)\b`)
	goOption      = regexp.MustCompile(`(?i)^(yes|approve|confirm|proceed|go ahead|post|send|create|publish|use (this|it))\b`)
	holdOption    = regexp.MustCompile(`(?i)^(no|revise|edit|let me|change|cancel|not yet|wait|hold)\b`)
)

const confirmationRefused = "The person already asked for this, so don't ask them to confirm it: do it now. " +
	"Misty shows its own approval card when their settings require one. Ask only about a choice that changes the result."

// confirmationQuestion reports whether a question only asks for a go-ahead.
func confirmationQuestion(question db.AgentQuestion) bool {
	if confirmHeader.MatchString(strings.TrimSpace(question.Header)) {
		return true
	}
	if len(question.Options) < 2 || !goOption.MatchString(strings.TrimSpace(question.Options[0].Label)) {
		return false
	}
	for _, option := range question.Options[1:] {
		if holdOption.MatchString(strings.TrimSpace(option.Label)) {
			return true
		}
	}
	return false
}
