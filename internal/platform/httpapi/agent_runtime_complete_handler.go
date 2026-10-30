package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (s *SpacesService) AgentRuntimeComplete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if isAIInvocationRuntimeID(chi.URLParam(r, "runID")) {
			s.agentRuntimeCompleteAIInvocation(w, r)
			return
		}
		var body struct {
			RuntimeRunID string          `json:"runtime_run_id"`
			Status       string          `json:"status"`
			Text         string          `json:"text"`
			Usage        json.RawMessage `json:"usage"`
			ErrorCode    string          `json:"error_code"`
			ErrorMessage string          `json:"error_message"`
		}
		if !readAgentRuntimeRequest(s.agentRuntime, w, r, &body) {
			return
		}
		runID := chi.URLParam(r, "runID")
		run, err := s.database.ValidatePersonalAgentTaskRuntime(r.Context(), runID, body.RuntimeRunID)
		if err != nil {
			existingRun, lookupErr := s.database.PersonalAgentRuntimeRecord(r.Context(), runID, body.RuntimeRunID)
			if lookupErr == nil && (existingRun.State == "completed" || existingRun.State == "completed_with_errors" || existingRun.State == "failed" || existingRun.State == "canceled") {
				if existingRun.State == "completed" || existingRun.SourceConversationID != "" || s.mentionRequestForRun(r.Context(), existingRun) != nil {
					if publishErr := s.publishPersonalAgentCompletion(r.Context(), existingRun, body.Text); publishErr != nil {
						writeAgentError(w, publishErr)
						return
					}
				}
				if projectionErr := s.completeLinkedAIInvocation(r.Context(), existingRun, body.Status, body.Text, body.ErrorMessage); projectionErr != nil {
					writeAgentError(w, projectionErr)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"run_id": existingRun.ID, "state": existingRun.State})
				return
			}
			writeAgentError(w, err)
			return
		}
		if body.Status == "success" {
			unconfirmed, checkErr := s.database.AgentRunHasUnconfirmedEffects(r.Context(), run.OwnerUserID, run.ID)
			if checkErr != nil {
				writeAgentError(w, checkErr)
				return
			}
			if unconfirmed {
				body.Status = "incomplete"
				body.ErrorCode = "unconfirmed_effects"
				body.ErrorMessage = "Some actions have not been confirmed. Review the run before retrying."
				body.Text = body.ErrorMessage
			}
		}
		body.Text = truncateAgentRuntimeText(strings.TrimSpace(body.Text), 12_000)
		if len(body.Usage) == 0 || !validJSONObject(body.Usage) {
			body.Usage = json.RawMessage(`{}`)
		}
		state, code, valid := personalAgentRuntimeCompletionOutcome(body.Status, body.ErrorCode)
		if !valid {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_completion_status"})
			return
		}
		message := body.Text
		if message == "" {
			message = strings.TrimSpace(body.ErrorMessage)
		}
		if message == "" {
			message = "Agent run failed"
		}
		result := MustAPIRawJSON(map[string]any{"text": body.Text, "usage": json.RawMessage(body.Usage), "runtime_status": body.Status, "message": message})
		finished, err := s.database.FinishSpaceRun(r.Context(), run.ID, state, result, code)
		if err != nil {
			writeAgentError(w, err)
			return
		}
		jobState := "completed"
		if state == "failed" {
			jobState = "failed"
		}
		if err := s.database.FinishDispatchedPersonalAgentTaskRunJob(r.Context(), run.ID, body.RuntimeRunID, jobState); err != nil {
			writeAgentError(w, err)
			return
		}
		if state == "completed" || finished.SourceConversationID != "" || s.mentionRequestForRun(r.Context(), finished) != nil {
			if err := s.publishPersonalAgentCompletion(r.Context(), finished, body.Text); err != nil {
				writeAgentError(w, err)
				return
			}
		}
		if err := s.completeLinkedAIInvocation(r.Context(), finished, body.Status, body.Text, body.ErrorMessage); err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"run_id": finished.ID, "state": finished.State})
	}
}
