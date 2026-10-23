package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kannachi323/misty/server/internal/workflow"
)

func (s *SpacesService) AgentRuntimeEvent() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if isAIInvocationRuntimeID(chi.URLParam(r, "runID")) {
			s.agentRuntimeEventAIInvocation(w, r)
			return
		}
		var body struct {
			RuntimeRunID string          `json:"runtime_run_id"`
			NodeID       string          `json:"node_id"`
			State        string          `json:"state"`
			Phase        string          `json:"phase"`
			Attempt      int             `json:"attempt"`
			Input        json.RawMessage `json:"input"`
			Progress     int             `json:"progress"`
			Output       json.RawMessage `json:"output"`
			ErrorCode    string          `json:"error_code"`
			ErrorMessage string          `json:"error_message"`
		}
		if !readAgentRuntimeRequest(s.agentRuntime, w, r, &body) {
			return
		}
		if len(body.NodeID) < 1 || len(body.NodeID) > 200 || len(body.Phase) < 1 || len(body.Phase) > 80 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_event"})
			return
		}
		run, err := s.database.ValidatePersonalAgentTaskRuntime(r.Context(), chi.URLParam(r, "runID"), body.RuntimeRunID)
		if err != nil {
			if strings.HasPrefix(body.NodeID, "model:") && body.State == "completed" {
				if stopped, lookupErr := s.database.PersonalAgentRuntimeRecord(r.Context(), chi.URLParam(r, "runID"), body.RuntimeRunID); lookupErr == nil && (stopped.State == "canceled" || stopped.State == "completed" || stopped.State == "completed_with_errors" || stopped.State == "failed") {
					if _, settleErr := s.meterPersonalAgentRuntimeModel(r.Context(), stopped, body.NodeID, workflow.StepCompleted, body.Output); settleErr != nil {
						writeAgentError(w, settleErr)
						return
					}
					writeJSON(w, http.StatusOK, map[string]bool{"accepted": true})
					return
				}
			}
			writeAgentError(w, err)
			return
		}
		state := workflow.StepState(body.State)
		switch state {
		case workflow.StepRunning, workflow.StepCompleted, workflow.StepFailed:
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_event_state"})
			return
		}
		if len(body.Output) == 0 || !validJSONObject(body.Output) {
			body.Output = json.RawMessage(`{}`)
		}
		if len(body.Input) == 0 || !validJSONObject(body.Input) {
			body.Input = json.RawMessage(`{}`)
		}
		body.Input = sanitizeAgentLifecycleJSON(body.Input)
		rawModelOutput := body.Output
		body.Output = sanitizeAgentLifecycleJSON(body.Output)
		var stepErr error
		if state == workflow.StepFailed {
			stepErr = errors.New(strings.TrimSpace(body.ErrorMessage))
		}
		if body.Attempt < 1 {
			body.Attempt = 1
		}
		overLimit := false
		if strings.HasPrefix(body.NodeID, "model:") {
			var err error
			if overLimit, err = s.meterPersonalAgentRuntimeModel(r.Context(), run, body.NodeID, state, rawModelOutput); err != nil {
				writeAgentError(w, err)
				return
			}
		}
		if err := s.database.CheckpointWorkflowStep(r.Context(), run.ID, workflow.StepEvent{NodeID: body.NodeID, State: state, Attempt: body.Attempt, Input: body.Input, Output: body.Output, Error: stepErr}); err != nil {
			writeAgentError(w, err)
			return
		}
		if err := s.database.TouchPersonalAgentTaskRuntime(r.Context(), run.ID, body.RuntimeRunID, body.Phase, body.Progress); err != nil {
			writeAgentError(w, err)
			return
		}
		s.projectLinkedAIInvocationEvent(r.Context(), run, body.NodeID, body.State, body.Phase, body.Output)
		writeJSON(w, http.StatusOK, map[string]bool{"accepted": true, "over_limit": overLimit})
	}
}
