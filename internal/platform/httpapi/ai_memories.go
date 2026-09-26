package api

import (
	"encoding/json"
	"errors"
	agent "github.com/kannachi323/misty/server/internal/agents"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *AIService) Memories() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
		if agentID == "" {
			identity, err := s.database.EnsureAskIdentity(r.Context(), userID, agent.FrontierDefaultModelID())
			if err != nil {
				writePersonalAgentError(w, err)
				return
			}
			agentID = identity.ID
		}
		items, err := s.database.MistyMemories(r.Context(), userID, "", 100, agentID)
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"memories": items})
	}
}

func (s *AIService) Memory() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodDelete && r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
		if agentID == "" {
			identity, err := s.database.EnsureAskIdentity(r.Context(), userID, agent.FrontierDefaultModelID())
			if err != nil {
				writePersonalAgentError(w, err)
				return
			}
			agentID = identity.ID
		}
		var err error
		if r.Method == http.MethodPut {
			var input struct {
				Content string `json:"content"`
				SpaceID string `json:"space_id"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input) != nil {
				http.Error(w, "Invalid preference", http.StatusBadRequest)
				return
			}
			err = s.database.UpdateAgentMemory(r.Context(), userID, agentID, "", chi.URLParam(r, "memoryID"), input.Content)
		} else {
			err = s.database.ForgetMistyMemory(r.Context(), userID, chi.URLParam(r, "memoryID"), agentID)
		}
		if errors.Is(err, db.ErrSpaceNotFound) {
			http.Error(w, "memory not found", http.StatusNotFound)
			return
		}
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
