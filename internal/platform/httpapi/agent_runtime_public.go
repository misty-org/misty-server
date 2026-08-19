package api

import (
	"encoding/json"
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

func (s *SpacesService) DecidePersonalAgentRunApproval() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, s.database)
		if err != nil || userID == "" {
			writeAgentRuntimeSessionError(w, err)
			return
		}
		var body struct {
			Decision string `json:"decision"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || (body.Decision != "approve" && body.Decision != "deny") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_approval_decision"})
			return
		}
		approved := body.Decision == "approve"
		item, err := s.database.DecideCreatorToolApproval(r.Context(), userID, chi.URLParam(r, "runID"), chi.URLParam(r, "approvalID"), approved)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		if err := s.agentRuntime.ResumeApproval(r.Context(), item.HookToken, item.RunID, item.ID, approved); err != nil {
			writeJSON(w, http.StatusAccepted, map[string]any{"approval": item, "runtime_resume_pending": true})
			return
		}
		if err := s.database.MarkCreatorToolApprovalResumed(r.Context(), item.RunID, item.ID); err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"approval": item, "runtime_resume_pending": false})
	}
}

func (s *SpacesService) AttachPersonalAgentRunContext() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, s.database)
		if err != nil || userID == "" {
			writeAgentRuntimeSessionError(w, err)
			return
		}
		var body struct {
			DeviceID     string          `json:"device_id"`
			Kind         string          `json:"kind"`
			OpaqueRef    string          `json:"opaque_ref"`
			DisplayName  string          `json:"display_name"`
			Capabilities json.RawMessage `json:"capabilities"`
			Metadata     json.RawMessage `json:"metadata"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_context"})
			return
		}
		item, err := s.database.AttachAgentRunContext(r.Context(), userID, chi.URLParam(r, "runID"), body.DeviceID, body.Kind, body.OpaqueRef, body.DisplayName, body.Capabilities, body.Metadata)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
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
