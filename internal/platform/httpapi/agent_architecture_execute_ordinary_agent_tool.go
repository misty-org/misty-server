package api

import (
	"encoding/json"
)



func spaceSearchAgentToolSchema() json.RawMessage {
	return TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer"}}})
}

func taskAgentToolSchema(write bool) json.RawMessage {
	properties := map[string]any{
		"query":          map[string]any{"type": "string"},
		"status":         map[string]any{"type": "string", "enum": []string{"todo", "in_progress", "done", "canceled"}},
		"assigneeUserId": map[string]any{"type": "string"},
		"from":           map[string]any{"type": "string"},
		"to":             map[string]any{"type": "string"},
	}
	if write {
		properties["id"] = map[string]any{"type": "string"}
		properties["title"] = map[string]any{"type": "string"}
		properties["notes"] = map[string]any{"type": "string"}
		properties["priority"] = map[string]any{"type": "string", "enum": []string{"low", "medium", "high"}}
		properties["dueAt"] = map[string]any{"type": "string"}
		properties["dueTimezone"] = map[string]any{"type": "string"}
		properties["version"] = map[string]any{"type": "integer"}
	}
	return TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": properties})
}
