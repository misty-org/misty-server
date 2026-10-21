package api

import (
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"net/http"
	"strings"
)

func (s *AIService) SteerInvocation() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			Text string `json:"text"`
			Key  string `json:"idempotencyKey"`
		}
		if decodeAIJSON(w, r, &body) != nil || strings.TrimSpace(body.Text) == "" || len(body.Text) > 8000 || body.Key == "" || len(body.Key) > 200 {
			http.Error(w, "invalid follow-up", 400)
			return
		}
		payload, _ := json.Marshal(map[string]string{"type": "user.steering", "text": body.Text})
		event, err := s.database.CommitAIInvocationEvent(r.Context(), userID, chi.URLParam(r, "invocationID"), "user-steering:"+body.Key, "user.steering", payload, "")
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "steering_unavailable", "message": "This task is finishing or unavailable. Your draft is preserved; send it after the task finishes."})
			return
		}
		// A message sent while the run waits on questions answers them by
		// superseding them; the waiting call returns and the run reads it.
		_ = s.database.SetAgentQuestionState(r.Context(), userID, "", chi.URLParam(r, "invocationID"), "superseded")
		writeJSON(w, http.StatusAccepted, map[string]any{"sequence": event.Sequence, "state": "queued"})
	}
}
// PauseInvocation and ResumeInvocation: the person takes over an agent's window and hands
// it back. The run waits at its next boundary while paused, spending no model turns.
func (s *AIService) PauseInvocation() http.HandlerFunc { return s.invocationHold("user.pause") }

func (s *AIService) ResumeInvocation() http.HandlerFunc { return s.invocationHold("user.resume") }

func (s *AIService) invocationHold(eventType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			Key string `json:"idempotencyKey"`
		}
		if decodeAIJSON(w, r, &body) != nil || body.Key == "" || len(body.Key) > 200 {
			http.Error(w, "invalid request", 400)
			return
		}
		payload, _ := json.Marshal(map[string]string{"type": eventType})
		if _, err := s.database.CommitAIInvocationEvent(r.Context(), userID, chi.URLParam(r, "invocationID"), eventType+":"+body.Key, eventType, payload, ""); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "task_unavailable", "message": "This task has finished or is unavailable."})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"state": eventType})
	}
}

func (s *SpacesService) AgentRuntimeSteering() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RuntimeRunID string `json:"runtime_run_id"`
			Boundary     string `json:"boundary"`
			Close        bool   `json:"close"`
		}
		if !readAgentRuntimeRequest(s.agentRuntime, w, r, &body) {
			return
		}
		if body.Boundary == "" || len(body.Boundary) > 100 {
			http.Error(w, "invalid boundary", 400)
			return
		}
		runID := chi.URLParam(r, "runID")
		var userID string
		if isAIInvocationRuntimeID(runID) {
			record, err := s.database.ValidateAIInvocationRuntime(r.Context(), runID, body.RuntimeRunID)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			userID = record.UserID
		} else {
			run, err := s.database.ValidatePersonalAgentTaskRuntime(r.Context(), runID, body.RuntimeRunID)
			if err != nil {
				writeAgentError(w, err)
				return
			}
			userID = run.OwnerUserID
		}
		batch, err := s.database.TakeAIInvocationSteering(r.Context(), userID, runID, body.Boundary, body.Close)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, 200, batch)
	}
}
