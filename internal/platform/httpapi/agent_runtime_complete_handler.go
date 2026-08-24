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
		run, task, err := s.database.ValidatePersonalAgentTaskRuntime(r.Context(), runID, body.RuntimeRunID)
		if err != nil {
			existingRun, existingTask, lookupErr := s.database.PersonalAgentTaskRuntimeRecord(r.Context(), runID, body.RuntimeRunID)
			if lookupErr == nil && (existingRun.State == "completed" || existingRun.State == "completed_with_errors" || existingRun.State == "failed" || existingRun.State == "canceled") {
				if existingRun.State == "completed" || existingRun.SourceConversationID != "" {
					if publishErr := s.publishPersonalAgentCompletion(r.Context(), existingRun, existingTask, body.Text); publishErr != nil {
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
		body.Text = truncateAgentRuntimeText(strings.TrimSpace(body.Text), 12_000)
		if len(body.Usage) == 0 || !validJSONObject(body.Usage) {
			body.Usage = json.RawMessage(`{}`)
		}
		done := run.SourceTaskID == ""
		if body.Status == "success" && run.SourceTaskID != "" {
			var doneErr error
			done, doneErr = s.database.PersonalAgentTaskDone(r.Context(), run.ID, body.RuntimeRunID)
			if doneErr != nil {
				writeAgentError(w, doneErr)
				return
			}
		}
		state, code, activityKind, valid := personalAgentRuntimeCompletionOutcome(body.Status, done, run.SourceTaskID != "", body.ErrorCode)
		if !valid {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_completion_status"})
			return
		}
		if body.Status == "success" && !done && body.Text == "" {
			body.Text = "The Agent stopped without marking the task done."
		}
		if err := s.settlePersonalAgentRuntimeUsage(r.Context(), run, body.Status, body.Usage); err != nil {
			writeAgentError(w, err)
			return
		}
		message := body.Text
		if message == "" {
			message = strings.TrimSpace(body.ErrorMessage)
		}
		if message == "" {
			message = "Agent run failed"
		}
		result := TestingMustAPIRawJSON(map[string]any{"text": body.Text, "usage": json.RawMessage(body.Usage), "runtime_status": body.Status, "message": message})
		if run.SourceTaskID != "" {
			if err := s.database.AddPersonalAgentRuntimeFinalActivity(r.Context(), run, task, activityKind, message, result); err != nil {
				writeAgentError(w, err)
				return
			}
		}
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
		if err := s.database.ReleasePersonalAgentRuntimeReservations(r.Context(), run.ID); err != nil {
			writeAgentError(w, err)
			return
		}
		if state == "completed" || finished.SourceConversationID != "" {
			if err := s.publishPersonalAgentCompletion(r.Context(), finished, task, body.Text); err != nil {
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
