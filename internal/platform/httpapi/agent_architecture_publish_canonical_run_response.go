package api

import (
	"encoding/json"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

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
