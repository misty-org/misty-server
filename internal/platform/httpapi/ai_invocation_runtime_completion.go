package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

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
	if body.Status != "success" && body.Status != "failed" && body.Status != "incomplete" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_completion_status"})
		return
	}
	record, err := s.database.ValidateAIInvocationRuntime(r.Context(), chi.URLParam(r, "runID"), body.RuntimeRunID)
	if err != nil {
		// Durable completion requests are idempotent after the invocation reaches a
		// terminal state.
		if existing, lookupErr := s.database.AIInvocationRuntimeRecord(r.Context(), chi.URLParam(r, "runID"), body.RuntimeRunID); lookupErr == nil && aiInvocationTerminal(existing.State) {
			// Stop may persist cancellation before the runtime reports its final
			// measured usage. Settle the original holds even though the task can no
			// longer publish a result; the stable group key makes callback replay safe.
			if settleErr := s.settleAIInvocationRuntimeUsage(existing, body.Status, body.Usage); settleErr != nil {
				// A permanent quota denial already made this task fail. Keep
				// retrying its accounting path, but acknowledge that same terminal
				// result instead of turning completion replay into another error.
				// Transient failures and canceled work still require reconciliation.
				if existing.State == "failed" && isHostedAILimitReached(settleErr) {
					writeJSON(w, http.StatusOK, map[string]any{"run_id": existing.ID, "state": existing.State, "code": "hosted_ai_limit_reached"})
					return
				}
				writeAgentError(w, settleErr)
				return
			}
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
	// Sub-agents still running when their parent finishes are stopped.
	s.cancelDelegatedChildren(context.WithoutCancel(r.Context()), record.UserID, record.ID)
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
			// The answer stays, with the warning after it: replacing it left the person with
			// only the warning for a run that had done its work (October 19).
			if strings.TrimSpace(body.Text) == "" {
				body.Text = body.ErrorMessage
			} else {
				body.Text = strings.TrimSpace(body.Text) + "\n\n" + body.ErrorMessage
			}
		}
	}
	// Count the run against its conversation's goal and continue it when due.
	// This waits for the invocation to settle on its own, after this callback.
	s.afterAIInvocationGoalRun(record, body.Status, body.Usage)
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
		message := incompleteAgentRuntimeText(body.Text)
		if _, err := s.aiInvocations.restoreDurable(r.Context(), *record); err != nil {
			writeAgentError(w, err)
			return
		}
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
	// A workflow run that only used the browser can be replayed next time.
	s.recordBrowserWorkflowReplay(context.WithoutCancel(r.Context()), record, prepared.body)
	writeJSON(w, http.StatusOK, map[string]any{"run_id": record.ID, "state": "completed"})
}

// A blocked task's explanation is its useful result even though the invocation
// must remain failed. Never replace that result with a success or expose a
// private context envelope in the terminal message.
func incompleteAgentRuntimeText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" || aiResponseLeaksContextEnvelope(text, "") {
		return "This request finished only partially. Review completed and uncertain actions before retrying."
	}
	return truncateAgentRuntimeText(text, 12_000)
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
	retitleAfterFirstReply(s.database, s.agent, userID, body, answer)
	return s.aiInvocations.complete(invocationID)
}
