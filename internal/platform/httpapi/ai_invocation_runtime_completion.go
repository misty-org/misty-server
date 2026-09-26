package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) agentRuntimeEventAIInvocation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RuntimeRunID string          `json:"runtime_run_id"`
		NodeID       string          `json:"node_id"`
		State        string          `json:"state"`
		Phase        string          `json:"phase"`
		Output       json.RawMessage `json:"output"`
	}
	if !readAgentRuntimeRequest(s.agentRuntime, w, r, &body) {
		return
	}
	record, err := s.database.ValidateAIInvocationRuntime(r.Context(), chi.URLParam(r, "runID"), body.RuntimeRunID)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	if body.State != "running" && body.State != "completed" && body.State != "failed" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_event_state"})
		return
	}
	if strings.HasPrefix(body.NodeID, "model:") {
		if err := s.meterAIInvocationRuntimeModel(r.Context(), record, body.NodeID, body.State, body.Output); err != nil {
			writeAgentError(w, err)
			return
		}
	}
	if record.SurfaceID == "routine" {
		writeAgentError(w, db.ErrSpaceInvalid)
		return
	}

	var eventErr error
	appendEvent := func(event aiInvocationEvent) {
		if eventErr != nil {
			return
		}
		receipt := body.RuntimeRunID + ":" + body.NodeID + ":" + body.State + ":" + event.Type
		eventErr = s.aiInvocations.appendReceipt(record.ID, event, receipt)
	}
	if s.aiInvocations != nil {
		if strings.HasPrefix(body.NodeID, "tool:") {
			toolName := strings.TrimPrefix(body.Phase, "using_")
			toolName = strings.ReplaceAll(toolName, "_", ".")
			eventType := "tool.completed"
			if body.State == "running" {
				eventType = "tool.started"
				appendEvent(aiInvocationEvent{Type: "assistant.status", Phase: "tool", Text: runtimeToolStatus(toolName)})
			} else if body.State == "failed" {
				eventType = "tool.failed"
			}
			appendEvent(aiInvocationEvent{Type: eventType, ToolCallID: strings.TrimPrefix(body.NodeID, "tool:"), ToolName: toolName})
		}
		if strings.HasPrefix(body.NodeID, "model:") && body.State == "completed" {
			var output struct {
				TextDelta string `json:"text_delta"`
			}
			if json.Unmarshal(body.Output, &output) == nil && strings.TrimSpace(output.TextDelta) != "" {
				appendEvent(aiInvocationEvent{Type: "response.delta", Delta: output.TextDelta})
			}
		}
	}
	if eventErr != nil {
		writeAgentError(w, eventErr)
		return
	}
	if err := s.database.TouchAIInvocationRuntime(r.Context(), record.ID, body.RuntimeRunID); err != nil {
		writeAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"accepted": true})
}

func (s *SpacesService) agentRuntimeCompleteAIInvocation(w http.ResponseWriter, r *http.Request) {
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
	if existing, lookupErr := s.database.AIInvocationRuntimeRecord(r.Context(), chi.URLParam(r, "runID"), body.RuntimeRunID); lookupErr == nil && existing.SurfaceID == "routine" {
		if err := s.retireRemovedInvocation(r.Context(), existing); err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"run_id": existing.ID, "accepted": true})
		return
	}
	if existing, lookupErr := s.database.AIInvocationRuntimeRecord(r.Context(), chi.URLParam(r, "runID"), body.RuntimeRunID); lookupErr == nil && existing.SurfaceID == "sdk" {
		if err := s.completeSDKInvocation(r.Context(), existing, false); err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"run_id": existing.ID, "accepted": true})
		return
	}
	record, err := s.database.ValidateAIInvocationRuntime(r.Context(), chi.URLParam(r, "runID"), body.RuntimeRunID)
	if err != nil {
		// Durable completion requests are idempotent after the invocation reaches a
		// terminal state.
		if existing, lookupErr := s.database.AIInvocationRuntimeRecord(r.Context(), chi.URLParam(r, "runID"), body.RuntimeRunID); lookupErr == nil && aiInvocationTerminal(existing.State) {
			if body.Status == "failed" {
				_ = s.completeAIInvocationRecap(r.Context(), existing, nil, "", errors.New(publicAgentRuntimeFailure(body.ErrorCode, body.ErrorMessage)))
			} else if prepared, prepareErr := s.prepareAIInvocationRuntime(r.Context(), existing); prepareErr == nil {
				_ = s.completeAIInvocationRecap(r.Context(), existing, prepared, strings.TrimSpace(body.Text), nil)
			}
			writeJSON(w, http.StatusOK, map[string]any{"run_id": existing.ID, "state": existing.State})
			return
		}
		writeAgentError(w, err)
		return
	}
	if s.aiInvocations == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "invocation_stream_unavailable"})
		return
	}
	if body.Status != "success" && body.Status != "failed" && body.Status != "incomplete" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_completion_status"})
		return
	}
	if body.Status == "success" {
		unconfirmed, checkErr := s.database.AgentRunHasUnconfirmedEffects(r.Context(), record.UserID, record.ID)
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
	if err := s.settleAIInvocationRuntimeUsage(record, body.Status, body.Usage); err != nil {
		if !isHostedAILimitReached(err) {
			writeAgentError(w, err)
			return
		}
		// A permanent accounting limit must terminate the invocation stream.
		// Otherwise the worker exits while the desktop keeps its page locked.
		message := publicAIInvocationError(err)
		if _, restoreErr := s.aiInvocations.restoreDurable(r.Context(), *record); restoreErr != nil {
			writeAgentError(w, restoreErr)
			return
		}
		if failErr := s.aiInvocations.fail(record.ID, message); failErr != nil {
			writeAgentError(w, failErr)
			return
		}
		_ = s.completeAIInvocationRecap(r.Context(), record, nil, "", err)
		writeJSON(w, http.StatusOK, map[string]any{"run_id": record.ID, "state": "failed", "code": "hosted_ai_limit_reached"})
		return
	}
	if body.Status == "incomplete" {
		message := "This request finished only partially. Review completed and uncertain actions before retrying."
		if err := s.aiInvocations.append(record.ID, aiInvocationEvent{Type: "assistant.message", Text: message, Summary: message}); err != nil {
			writeAgentError(w, err)
			return
		}
		if err := s.aiInvocations.fail(record.ID, message); err != nil {
			writeAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"run_id": record.ID, "state": "failed", "partial": true})
		return
	}
	if body.Status == "failed" {
		message := publicAgentRuntimeFailure(body.ErrorCode, body.ErrorMessage)
		if err := s.aiInvocations.fail(record.ID, message); err != nil {
			writeAgentError(w, err)
			return
		}
		_ = s.completeAIInvocationRecap(r.Context(), record, nil, "", errors.New(message))
		writeJSON(w, http.StatusOK, map[string]any{"run_id": record.ID, "state": "failed"})
		return
	}
	prepared, err := s.prepareAIInvocationRuntime(r.Context(), record)
	if err != nil {
		s.aiInvocations.fail(record.ID, publicAIInvocationError(err))
		writeAgentError(w, err)
		return
	}
	if err := s.finishAIInvocationRuntimeAnswer(record.UserID, record.ID, prepared.body, strings.TrimSpace(body.Text), prepared.resolved, prepared.prompt); err != nil {
		s.aiInvocations.fail(record.ID, publicAIInvocationError(err))
		_ = s.completeAIInvocationRecap(r.Context(), record, prepared, "", err)
		writeAgentError(w, err)
		return
	}
	if err := s.completeAIInvocationRecap(r.Context(), record, prepared, strings.TrimSpace(body.Text), nil); err != nil {
		writeAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run_id": record.ID, "state": "completed"})
}

func (s *SpacesService) finishAIInvocationRuntimeAnswer(userID, invocationID string, body aiInvocationInput, answer string, resolved []aiResolvedContext, compiledPrompt string) error {
	if answer == "" {
		return errors.New("agent runtime returned an empty response")
	}
	if aiResponseLeaksContextEnvelope(answer, compiledPrompt) {
		return errors.New("agent runtime returned the private context envelope")
	}
	artifactKind := strings.TrimSpace(body.RequestedArtifactKind)
	if artifactKind == "" {
		artifactKind = strings.TrimSpace(inferredAIArtifactKind(body))
	}
	if artifactKind == "" && body.Selection != nil && strings.HasPrefix(body.SurfaceID, "notes") {
		artifactKind = "text_patch"
	}
	if artifactKind == "text_patch" && body.Selection != nil {
		artifact := s.aiInvocations.addTextPatchArtifact(userID, invocationID, answer, resolved, body)
		if err := s.aiInvocations.append(invocationID, aiInvocationEvent{Type: "assistant.message", Text: "I prepared a revision.", Summary: "I prepared a revision."}); err != nil {
			return err
		}
		if err := s.aiInvocations.append(invocationID, aiInvocationEvent{Type: "artifact.proposed", Artifact: artifact}); err != nil {
			return err
		}
		if err := s.aiInvocations.append(invocationID, aiInvocationEvent{Type: "approval.required", Artifact: artifact}); err != nil {
			return err
		}
	} else if artifactKind == "task_set" {
		tasks, parseErr := parseAITaskDrafts(answer)
		if parseErr != nil || len(tasks) == 0 {
			if err := s.aiInvocations.append(invocationID, aiInvocationEvent{Type: "assistant.message", Text: "I did not find concrete tasks that were safe to propose.", Summary: "No tasks proposed."}); err != nil {
				return err
			}
		} else if artifact := s.aiInvocations.addTaskSetArtifact(userID, invocationID, tasks, resolved, body); artifact != nil {
			suffix := "s"
			if len(tasks) == 1 {
				suffix = ""
			}
			message := fmt.Sprintf("I prepared %d task%s for review.", len(tasks), suffix)
			if err := s.aiInvocations.append(invocationID, aiInvocationEvent{Type: "assistant.message", Text: message, Summary: message}); err != nil {
				return err
			}
			if err := s.aiInvocations.append(invocationID, aiInvocationEvent{Type: "artifact.proposed", Artifact: artifact}); err != nil {
				return err
			}
			if err := s.aiInvocations.append(invocationID, aiInvocationEvent{Type: "approval.required", Artifact: artifact}); err != nil {
				return err
			}
		} else {
			return errors.New("no authorized Space could receive the proposed tasks")
		}
	} else if spec, ok := aiArtifactSpecs[artifactKind]; ok {
		summary, operations, parseErr := parseAIStructuredArtifact(answer)
		if parseErr != nil {
			return parseErr
		}
		artifact := s.aiInvocations.addStructuredArtifact(userID, invocationID, artifactKind, summary, operations, resolved, body, spec)
		message := strings.TrimSpace(summary)
		if message == "" {
			message = "I prepared a reviewable proposal. Nothing has been applied."
		}
		if err := s.aiInvocations.append(invocationID, aiInvocationEvent{Type: "assistant.message", Text: message, Summary: message}); err != nil {
			return err
		}
		if err := s.aiInvocations.append(invocationID, aiInvocationEvent{Type: "artifact.proposed", Artifact: artifact}); err != nil {
			return err
		}
		if err := s.aiInvocations.append(invocationID, aiInvocationEvent{Type: "approval.required", Artifact: artifact}); err != nil {
			return err
		}
	} else {
		if err := s.aiInvocations.append(invocationID, aiInvocationEvent{Type: "assistant.message", Text: answer, Summary: aiConciseSummary(answer)}); err != nil {
			return err
		}
	}
	return s.aiInvocations.complete(invocationID)
}
