package api

import (
	"context"
	"encoding/json"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
)

func publishCanonicalRunResponse(ctx context.Context, database *db.Database, runtime *serveragent.Service, userID string, run *db.SpaceRun) error {
	if run == nil || run.SourceConversationID == "" || run.State != "completed" && run.State != "completed_with_errors" && run.State != "failed" && run.State != "canceled" && run.State != "rejected" {
		return nil
	}
	actionID, claimed, err := database.ClaimRunResponsePublication(ctx, run.ID)
	if err != nil || !claimed {
		return err
	}
	_, text := TestingCanonicalRunResponse(run)
	details := map[string]string{"source_type": run.SourceType, "source_conversation_id": run.SourceConversationID}
	finish := func(deliveryErr error) error {
		state := "completed"
		if deliveryErr != nil {
			state = "failed"
			details["error"] = deliveryErr.Error()
		}
		if updateErr := database.FinishRunResponsePublication(ctx, actionID, state, TestingMustAPIRawJSON(details)); updateErr != nil && deliveryErr == nil {
			return updateErr
		}
		return deliveryErr
	}
	switch run.SourceType {
	case "direct", "group_mention":
		runes := []rune(text)
		if len(runes) > db.MaxMessageChars {
			runes = runes[:db.MaxMessageChars]
		}
		var reply *db.SpaceMessage
		var selectedGroup bool
		selectedGroup, err = database.IsSpaceConversationForMember(ctx, userID, run.SpaceID, run.SourceConversationID)
		if err == nil && selectedGroup {
			reply, err = database.CreateSpaceConversationAgentMessage(ctx, userID, run.SpaceID, run.SourceConversationID, run.AgentID, string(runes))
		} else if err == nil {
			reply, err = database.CreateSpaceAgentMessage(ctx, userID, run.SpaceID, run.AgentID, string(runes))
		}
		if err == nil {
			details["message_id"] = reply.ID
		}
	default:
		details["status"] = "no_conversation_delivery_required"
	}
	return finish(err)
}

func TestingCanonicalRunResponse(run *db.SpaceRun) (string, string) {
	if run.State == "failed" {
		message := strings.TrimSpace(run.ErrorMessage)
		if message == "" {
			message = "The agent run failed."
		}
		return "error", message
	}
	if run.State == "canceled" {
		return "agent_message", "The isolated run was canceled."
	}
	if run.State == "rejected" {
		return "agent_message", "The requested action was rejected, so the Agent stopped the run."
	}
	var output map[string]any
	_ = json.Unmarshal(run.Outputs, &output)
	if text, ok := output["text"].(string); ok && strings.TrimSpace(text) != "" {
		return "agent_message", strings.TrimSpace(text)
	}
	if run.State == "running" || run.State == "cooldown" || run.State == "queued" {
		return "agent_message", "The isolated run is in progress. Track run " + run.ID + " in Studio."
	}
	if run.State == "completed_with_errors" {
		return "agent_message", "The isolated run completed with item errors. Open run " + run.ID + " in Studio for the successful outputs and failed items."
	}
	return "agent_message", "The isolated run completed. Open run " + run.ID + " in Studio to inspect its output and actions."
}

func TestingPromptFromRun(run *db.SpaceRun) string {
	var input map[string]any
	_ = json.Unmarshal(run.Input, &input)
	prompt, _ := input["prompt"].(string)
	return prompt
}

func TestingMustAPIRawJSON(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
