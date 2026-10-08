package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// aiProgressItem is one entry of a reply's work, in order: something Misty
// said while working, or a tool it ran.
type aiProgressItem struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	State string `json:"state,omitempty"`
	// What a tool targeted and got back, how long it took, and why it failed.
	Activity   *aiToolActivity `json:"activity,omitempty"`
	DurationMS int64           `json:"durationMs,omitempty"`
	Error      string          `json:"error,omitempty"`
}

// maxProgressItems bounds a long run's timeline; the newest work is kept.
const maxProgressItems = 200

// InvocationProgress returns how a finished or running reply got its answer,
// rebuilt from the invocation's stored events, so a reloaded conversation shows
// the same progress the person watched live.
func (s *AIService) InvocationProgress() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		events, _, err := s.database.AIInvocationEvents(r.Context(), userID, chi.URLParam(r, "invocationID"), 0)
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": aiInvocationProgress(events)})
	}
}

// InvocationState returns only whether a request is still going. A device
// running another device's browser session uses it to close the session when
// the request is stopped (docs/design/agents-on-devices/BRIEF.md).
func (s *AIService) InvocationState() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		record, err := s.database.AIInvocationByID(r.Context(), userID, chi.URLParam(r, "invocationID"))
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]string{"state": record.State})
	}
}

// aiInvocationProgress walks the events in order. Text the model wrote before
// calling a tool becomes a note; the text after the last tool is the answer
// itself, which the conversation already shows, so it is left out.
func aiInvocationProgress(events []db.AIInvocationEventRecord) []aiProgressItem {
	items := []aiProgressItem{}
	pending := ""
	tools := map[string]int{}
	for _, event := range events {
		var payload struct {
			Delta      string          `json:"delta"`
			ToolCallID string          `json:"toolCallId"`
			ToolName   string          `json:"toolName"`
			Activity   *aiToolActivity `json:"activity"`
			DurationMS int64           `json:"durationMs"`
			Error      string          `json:"error"`
		}
		if json.Unmarshal(event.Payload, &payload) != nil {
			continue
		}
		switch event.EventType {
		case "response.delta":
			pending += payload.Delta
		case "tool.started":
			if note := strings.TrimSpace(pending); note != "" {
				items = append(items, aiProgressItem{Type: "note", Text: truncateAgentRuntimeText(note, 4000)})
			}
			pending = ""
			if _, seen := tools[payload.ToolCallID]; seen && payload.ToolCallID != "" {
				continue
			}
			tools[payload.ToolCallID] = len(items)
			items = append(items, aiProgressItem{Type: "tool", ID: payload.ToolCallID, Name: payload.ToolName, State: "running"})
		case "tool.completed", "tool.failed":
			index, seen := tools[payload.ToolCallID]
			if !seen {
				continue
			}
			items[index].State = strings.TrimPrefix(event.EventType, "tool.")
			items[index].DurationMS = payload.DurationMS
			if event.EventType == "tool.failed" {
				items[index].Error = payload.Error
			}
		case "tool.detail":
			if index, seen := tools[payload.ToolCallID]; seen {
				items[index].Activity = payload.Activity
			}
		case "invocation.failed", "invocation.canceled":
			// A tool still marked running never finished.
			for i := range items {
				if items[i].Type == "tool" && items[i].State == "running" {
					items[i].State = "failed"
				}
			}
		}
	}
	if len(items) > maxProgressItems {
		items = items[len(items)-maxProgressItems:]
	}
	return items
}
