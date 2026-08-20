package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *AIService) Settings() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		switch r.Method {
		case http.MethodGet:
			settings, preferences, err := s.database.AISettings(r.Context(), userID)
			if err != nil {
				TestingWriteAIError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"settings": settings, "preferences": preferences})
		case http.MethodPut:
			var body struct {
				Enabled       bool `json:"enabled"`
				RetentionDays int  `json:"retention_days"`
			}
			if decodeAIJSON(w, r, &body) != nil {
				return
			}
			settings, err := s.database.UpdateAISettings(r.Context(), userID, body.Enabled, body.RetentionDays)
			if err != nil {
				TestingWriteAIError(w, err)
				return
			}
			if !settings.Enabled {
				s.invocations.cancelAllForUser(userID)
			}
			writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (s *AIService) SurfacePreference() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			PinnedAgentID string          `json:"pinned_agent_id"`
			Proactive     bool            `json:"proactive_enabled"`
			SavedActions  json.RawMessage `json:"saved_actions"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		surfaceID := strings.TrimSpace(chi.URLParam(r, "surfaceID"))
		if !aiSurfaceIDs[surfaceID] || !validAISavedActions(body.SavedActions) {
			http.Error(w, "invalid surface preference", http.StatusBadRequest)
			return
		}
		preference, err := s.database.UpsertAISurfacePreference(r.Context(), userID, db.AISurfacePreference{
			SurfaceID: surfaceID, PinnedAgentID: strings.TrimSpace(body.PinnedAgentID),
			Proactive: body.Proactive, SavedActions: body.SavedActions,
		})
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"preference": preference})
	}
}

func validAISavedActions(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	if len(raw) > 64<<10 {
		return false
	}
	var actions []struct {
		ID                    string `json:"id"`
		Label                 string `json:"label"`
		Prompt                string `json:"prompt"`
		RequestedArtifactKind string `json:"requested_artifact_kind,omitempty"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&actions) != nil || len(actions) > 20 {
		return false
	}
	seen := map[string]bool{}
	for _, action := range actions {
		action.ID, action.Label, action.Prompt = strings.TrimSpace(action.ID), strings.TrimSpace(action.Label), strings.TrimSpace(action.Prompt)
		if action.ID == "" || seen[action.ID] || len(action.ID) > 100 || action.Label == "" || len([]rune(action.Label)) > 80 || action.Prompt == "" || len(action.Prompt) > 8<<10 {
			return false
		}
		seen[action.ID] = true
		if action.RequestedArtifactKind != "" && action.RequestedArtifactKind != "text_patch" && action.RequestedArtifactKind != "task_set" {
			if _, ok := aiArtifactSpecs[action.RequestedArtifactKind]; !ok {
				return false
			}
		}
	}
	return true
}

func (s *AIService) InvocationFeedback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			Rating  int    `json:"rating"`
			Reason  string `json:"reason"`
			Comment string `json:"comment"`
		}
		if decodeAIJSON(w, r, &body) != nil {
			return
		}
		if err := s.database.RecordAIFeedback(r.Context(), userID, chi.URLParam(r, "invocationID"), body.Rating, body.Reason, body.Comment); err != nil {
			TestingWriteAIError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
