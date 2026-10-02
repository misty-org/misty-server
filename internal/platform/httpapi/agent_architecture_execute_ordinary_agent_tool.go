package api

import (
	"encoding/json"
	"net/http"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
)

func providerAgentToolSchema(provider string, write bool) json.RawMessage {
	properties := map[string]any{"query": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer"}, "resource": map[string]any{"type": "string"}}
	if write {
		properties["destination"] = map[string]any{"type": "string"}
		properties["payload"] = map[string]any{"type": "object"}
		properties["mode"] = map[string]any{"type": "string", "enum": []string{"draft", "send"}}
		if provider == "github" {
			properties["workspace_id"] = map[string]any{"type": "string"}
			properties["action"] = map[string]any{"type": "string", "enum": []string{"create_issue", "comment_issue", "create_branch", "create_pull_request"}}
			return TestingMustAPIRawJSON(map[string]any{"type": "object", "required": []string{"workspace_id", "action", "payload"}, "properties": properties})
		}
		if provider == "figma" {
			properties["binding_id"] = map[string]any{"type": "string"}
			properties["file_key"] = map[string]any{"type": "string"}
			properties["message"] = map[string]any{"type": "string", "maxLength": 5000}
			properties["node_id"] = map[string]any{"type": "string"}
			return TestingMustAPIRawJSON(map[string]any{"type": "object", "required": []string{"binding_id", "message"}, "properties": properties})
		}
	}
	return TestingMustAPIRawJSON(map[string]any{"type": "object", "properties": properties})
}

func providerSupportsWrite(provider string) bool {
	switch provider {
	case "github", "figma":
		return true
	}
	return false
}

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

func (s *SpacesService) WorkflowVersions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, workflowID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "workflowID")
		if r.Method == http.MethodGet {
			items, err := s.database.WorkflowVersions(r.Context(), userID, spaceID, workflowID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"versions": items})
			return
		}
		var body struct {
			Version    string              `json:"version"`
			Metadata   db.WorkflowMetadata `json:"metadata"`
			Definition json.RawMessage     `json:"definition"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := s.database.CreateWorkflowVersion(r.Context(), userID, spaceID, workflowID, body.Version, body.Metadata, body.Definition)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	}
}
