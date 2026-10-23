package api

import (
	"encoding/json"
	"errors"
	"github.com/kannachi323/misty/server/internal/agent"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/platform/db"
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
		space, err := requestMemorySpace(r.Context(), s.database, userID, memorySpaceParam(r))
		if err != nil {
			writeMemorySpaceError(w, err)
			return
		}
		items, err := s.database.MistyMemories(r.Context(), userID, space, 100, agentID)
		if err != nil {
			WriteAIError(w, err)
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
		// The memory's Space is the window's (?space_id); a memory never moves.
		space, err := requestMemorySpace(r.Context(), s.database, userID, memorySpaceParam(r))
		if err != nil {
			writeMemorySpaceError(w, err)
			return
		}
		if r.Method == http.MethodPut {
			var input struct {
				Content string `json:"content"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input) != nil {
				http.Error(w, "Invalid preference", http.StatusBadRequest)
				return
			}
			err = s.database.UpdateAgentMemory(r.Context(), userID, agentID, space, chi.URLParam(r, "memoryID"), input.Content)
		} else {
			err = s.database.ForgetMistyMemory(r.Context(), userID, space, chi.URLParam(r, "memoryID"), agentID)
		}
		if errors.Is(err, db.ErrSpaceNotFound) {
			http.Error(w, "memory not found", http.StatusNotFound)
			return
		}
		if err != nil {
			WriteAIError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
