package api

import (
	"context"
	"strings"
	"unicode/utf8"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func personalAgentRuntimeCompletionOutcome(status, errorCode string) (state, code string, valid bool) {
	switch status {
	case "success":
		return "completed", "", true
	case "incomplete":
		code = strings.TrimSpace(errorCode)
		if code == "" {
			code = "run_incomplete"
		}
		return "completed_with_errors", code, true
	case "failed":
		code = strings.TrimSpace(errorCode)
		if code == "" {
			code = "agent_runtime_failed"
		}
		return "failed", code, true
	default:
		return "", "", false
	}
}

func (s *SpacesService) publishPersonalAgentCompletion(ctx context.Context, run *db.SpaceRun, text string) error {
	if request := s.mentionRequestForRun(ctx, run); request != nil {
		return s.publishMentionReply(ctx, run, request, text)
	}
	actionID, claimed, err := s.database.ClaimRunResponsePublication(ctx, run.ID)
	if err != nil || !claimed {
		return err
	}
	summary := truncateAgentRuntimeText(strings.TrimSpace(text), 600)
	if summary == "" {
		summary = "Finished the requested work."
	}
	var message *db.SpaceMessage
	var publishErr error
	if run.SourceConversationID != "" {
		messageText := summary
		if run.State != "completed" {
			messageText = "I couldn't finish that. " + summary
		}
		message, publishErr = s.database.CreatePersonalAgentConversationRunMessage(ctx, run.OwnerUserID, run.SpaceID, run.SourceConversationID, run.AgentID, messageText, run.ID, run.SourceMessageID)
	} else {
		message, publishErr = s.database.CreatePersonalAgentSpaceMessage(ctx, run.OwnerUserID, run.SpaceID, run.AgentID, "Completed: "+summary)
	}
	details := TestingMustAPIRawJSON(map[string]any{"run_id": run.ID})
	state := "failed"
	if publishErr == nil {
		state = "completed"
		details = TestingMustAPIRawJSON(map[string]any{"run_id": run.ID, "message_id": message.ID})
	}
	if finishErr := s.database.FinishRunResponsePublication(ctx, actionID, state, details); finishErr != nil {
		return finishErr
	}
	return publishErr
}

func truncateAgentRuntimeText(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}

func TestingPersonalAgentRuntimeCompletionOutcome(status, errorCode string) (string, string, bool) {
	return personalAgentRuntimeCompletionOutcome(status, errorCode)
}
