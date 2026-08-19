package api

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"unicode/utf8"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func personalAgentRuntimeCompletionOutcome(status string, taskDone, taskRun bool, errorCode string) (state, code, activityKind string, valid bool) {
	switch status {
	case "success":
		if taskDone {
			return "completed", "", "result", true
		}
		return "completed_with_errors", "task_not_completed", "failure", true
	case "incomplete":
		if taskRun {
			return "completed_with_errors", "task_not_completed", "failure", true
		}
		code = strings.TrimSpace(errorCode)
		if code == "" {
			code = "run_incomplete"
		}
		return "completed_with_errors", code, "failure", true
	case "failed":
		code = strings.TrimSpace(errorCode)
		if code == "" {
			code = "agent_runtime_failed"
		}
		return "failed", code, "failure", true
	default:
		return "", "", "", false
	}
}

func (s *SpacesService) publishPersonalAgentTaskCompletion(ctx context.Context, run *db.SpaceRun, task *db.SpaceTask, text string) error {
	actionID, claimed, err := s.database.ClaimRunResponsePublication(ctx, run.ID)
	if err != nil || !claimed {
		return err
	}
	summary := truncateAgentRuntimeText(strings.TrimSpace(text), 600)
	if summary == "" {
		summary = "Finished the assigned work."
	}
	taskLink := "/spaces/" + url.PathEscape(run.SpaceID) + "/planner/tasks/board?task=" + url.QueryEscape(task.ID)
	message, publishErr := s.database.CreatePersonalAgentSpaceMessage(ctx, run.RequestingMemberID, run.SpaceID, run.AgentID, "Completed ["+task.TaskKey+"]("+taskLink+"): "+summary)
	details := TestingMustAPIRawJSON(map[string]any{"task_id": task.ID, "task_key": task.TaskKey})
	state := "failed"
	if publishErr == nil {
		state = "completed"
		var values map[string]any
		_ = json.Unmarshal(details, &values)
		values["message_id"] = message.ID
		details = TestingMustAPIRawJSON(values)
	}
	if finishErr := s.database.FinishRunResponsePublication(ctx, actionID, state, details); finishErr != nil {
		return finishErr
	}
	return publishErr
}

func (s *SpacesService) publishPersonalAgentCompletion(ctx context.Context, run *db.SpaceRun, task *db.SpaceTask, text string) error {
	space, err := s.database.SpaceByID(ctx, run.OwnerUserID, run.SpaceID)
	if err != nil {
		return err
	}
	// The private Misty workspace intentionally has no shared chat. The result
	// remains available on the owner-only Agents page.
	if space.Kind == "misty" && run.SourceConversationID == "" {
		return nil
	}
	if run.SourceTaskID != "" {
		return s.publishPersonalAgentTaskCompletion(ctx, run, task, text)
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

func TestingPersonalAgentRuntimeCompletionOutcome(status string, taskDone, taskRun bool, errorCode string) (string, string, string, bool) {
	return personalAgentRuntimeCompletionOutcome(status, taskDone, taskRun, errorCode)
}
