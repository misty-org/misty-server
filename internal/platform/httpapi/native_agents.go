package api

import (
	"errors"
	"github.com/go-chi/chi/v5"
	agent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"net/http"
)

func writePersonalAgentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrPersonalAgentNotFound):
		http.Error(w, "agent not found", http.StatusNotFound)
	case errors.Is(err, db.ErrPersonalAgentConflict):
		http.Error(w, "agent changed; reload before saving", http.StatusConflict)
	default:
		writeSpaceError(w, err)
	}
}

func (s *AIService) PersonalAgents() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if db.AppAuthorityFromContext(r.Context()) != nil {
			http.Error(w, "Host-only agent management", http.StatusForbidden)
			return
		}
		if !ok {
			return
		}
		if r.Method == http.MethodGet {
			if _, err := s.database.EnsureAskIdentity(r.Context(), userID, agent.FrontierDefaultModelID()); err != nil {
				writePersonalAgentError(w, err)
				return
			}
			items, err := s.database.PersonalAgents(r.Context(), userID)
			if err != nil {
				writePersonalAgentError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"agents": items})
			return
		}
		var body db.AgentProfileInput
		body.Enabled = true
		if err := decodeAIJSON(w, r, &body); err != nil {
			http.Error(w, "invalid agent", http.StatusBadRequest)
			return
		}
		item, err := s.database.SavePersonalAgent(r.Context(), userID, "", body)
		if err != nil {
			writePersonalAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	}
}

func (s *AIService) PersonalAgent() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if db.AppAuthorityFromContext(r.Context()) != nil {
			http.Error(w, "Host-only agent management", http.StatusForbidden)
			return
		}
		if !ok {
			return
		}
		id := chi.URLParam(r, "agentID")
		if r.Method == http.MethodDelete {
			if err := s.database.DeletePersonalAgent(r.Context(), userID, id); err != nil {
				writePersonalAgentError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var body db.AgentProfileInput
		if err := decodeAIJSON(w, r, &body); err != nil {
			http.Error(w, "invalid agent", http.StatusBadRequest)
			return
		}
		item, err := s.database.SavePersonalAgent(r.Context(), userID, id, body)
		if err != nil {
			writePersonalAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func (s *AIService) AgentExecutionLease() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		if db.AppAuthorityFromContext(r.Context()) != nil {
			http.Error(w, "Host-only execution activation", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodDelete {
			if err := s.database.ReleaseAgentExecution(r.Context(), userID, chi.URLParam(r, "taskID")); err != nil {
				writePersonalAgentError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var input db.AgentExecutionLease
		if decodeAIJSON(w, r, &input) != nil {
			http.Error(w, "invalid execution", http.StatusBadRequest)
			return
		}
		if err := s.database.AcquireAgentExecution(r.Context(), userID, input); err != nil {
			writePersonalAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"task_id": input.TaskID})
	}
}
