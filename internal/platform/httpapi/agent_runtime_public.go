package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *SpacesService) PersonalAgentRunDetail() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, s.database)
		if err != nil || userID == "" {
			writeAgentRuntimeSessionError(w, err)
			return
		}
		item, err := s.database.PersonalAgentRunDetailForOwner(r.Context(), userID, chi.URLParam(r, "runID"))
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func (s *SpacesService) CancelPersonalAgentRun() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, s.database)
		if err != nil || userID == "" {
			writeAgentRuntimeSessionError(w, err)
			return
		}
		run, err := s.database.CancelPersonalAgentTaskRunForOwner(r.Context(), userID, chi.URLParam(r, "runID"))
		if err != nil {
			writeAgentError(w, err)
			return
		}
		cancelPending := false
		if s.agentRuntime.Enabled() && run.RuntimeRunID != "" {
			cancelPending = s.agentRuntime.Cancel(r.Context(), run.RuntimeRunID, run.ID) != nil
		}
		writeJSON(w, http.StatusOK, map[string]any{"run": run, "runtime_cancel_pending": cancelPending})
	}
}

func writeAgentRuntimeSessionError(w http.ResponseWriter, err error) {
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Error(w, "not authenticated", http.StatusUnauthorized)
}
