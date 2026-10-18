package api

import (
	"testing"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestConfirmationQuestionsAreRecognized(t *testing.T) {
	options := func(labels ...string) []db.AgentQuestionOption {
		out := make([]db.AgentQuestionOption, len(labels))
		for i, label := range labels {
			out[i] = db.AgentQuestionOption{Label: label}
		}
		return out
	}
	for _, question := range []db.AgentQuestion{
		{Header: "Confirm post", Options: options("Yes", "No")},
		{Header: "Issue draft", Options: options("Use this draft (Recommended)", "Revise the draft")},
		{Header: "Slack", Options: options("Approve and post (Recommended)", "Let me edit it")},
	} {
		if !confirmationQuestion(question) {
			t.Errorf("not refused: %+v", question)
		}
	}
	for _, question := range []db.AgentQuestion{
		{Header: "Channel", Options: options("Post to #general (Recommended)", "Post to #marketing")},
		{Header: "Format", Options: options("Short", "Detailed")},
	} {
		if confirmationQuestion(question) {
			t.Errorf("refused a real choice: %+v", question)
		}
	}
}
