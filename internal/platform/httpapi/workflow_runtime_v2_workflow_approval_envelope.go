package api

import (
	"encoding/json"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestingWorkflowApprovalEnvelope(run *db.SpaceRun, actionKind, provider, connectionID, destination string, input json.RawMessage) json.RawMessage {
	var decoded any
	_ = json.Unmarshal(input, &decoded)
	reason := TestingFindWorkflowString(decoded, "reason", "rationale", "completionCriteria", "completion_criteria")
	if reason == "" {
		reason = "The Agent needs this action to continue the pinned workflow run."
	}
	return TestingMustAPIRawJSON(map[string]any{
		"agent_id":            run.AgentID,
		"agent_version_id":    run.AgentVersionID,
		"workflow_version_id": run.WorkflowVersionID,
		"run_id":              run.ID,
		"action_kind":         actionKind,
		"provider":            provider,
		"connection_id":       connectionID,
		"destination":         destination,
		"bot_identity":        map[string]string{"name": "Misty", "provider": provider},
		"content_preview":     extractWorkflowText(input),
		"reason":              reason,
		"affected_resources":  []string{destination},
		"citations":           findWorkflowValue(decoded, "citations"),
		"input":               json.RawMessage(input),
		"reversibility":       "Provider actions may not be reversible after execution.",
	})
}

func findWorkflowValue(value any, key string) any {
	switch item := value.(type) {
	case map[string]any:
		if found, ok := item[key]; ok {
			return found
		}
		for _, child := range item {
			if found := findWorkflowValue(child, key); found != nil {
				return found
			}
		}
	case []any:
		for _, child := range item {
			if found := findWorkflowValue(child, key); found != nil {
				return found
			}
		}
	}
	return nil
}

func TestingFindContentInput(value any) map[string]any {
	switch item := value.(type) {
	case map[string]any:
		if _, ok := item["contentRef"]; ok {
			return item
		}
		for _, child := range item {
			if found := TestingFindContentInput(child); found != nil {
				return found
			}
		}
	case []any:
		for _, child := range item {
			if found := TestingFindContentInput(child); found != nil {
				return found
			}
		}
	}
	return nil
}
