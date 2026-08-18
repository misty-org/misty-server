package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (s *SpacesService) PersonalAgentActivity() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, s.database)
		if err != nil || userID == "" {
			writeAgentRuntimeSessionError(w, err)
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		page, err := s.database.PersonalAgentActivity(r.Context(), userID, chi.URLParam(r, "agentID"), strings.TrimSpace(r.URL.Query().Get("cursor")), limit)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
	}
}

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

func (s *SpacesService) RetryPersonalAgentRun() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, s.database)
		if err != nil || userID == "" {
			writeAgentRuntimeSessionError(w, err)
			return
		}
		run, err := s.database.RetryPersonalAgentTaskRunForOwner(r.Context(), userID, chi.URLParam(r, "runID"))
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, run)
	}
}

func writeAgentRuntimeSessionError(w http.ResponseWriter, err error) {
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Error(w, "not authenticated", http.StatusUnauthorized)
}
