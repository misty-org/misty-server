package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (s *AIService) CreateInvocation() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body aiInvocationInput
		if !decodeAIInvocationBody(w, r, &body) {
			return
		}
		// Only the server links a run to a parent's notebook or delegation.
		body.NotebookKey, body.DelegatedFrom, body.CodingProject = "", "", ""
		scheduled, ok := s.admitScheduledInvocation(w, r, userID, &body)
		if !ok {
			return
		}
		admittedRun := false
		defer func() {
			if scheduled != nil && !admittedRun {
				scheduled.release(context.WithoutCancel(r.Context()), s.database, errors.New("the scheduled run couldn't start on its computer"))
			}
		}()
		if !s.validateInvocationRequest(w, r, userID, &body) {
			return
		}
		if !ensureIdempotencyKey(w, r, &body) {
			return
		}
		actionID := body.RequestedArtifactKind
		if actionID == "" {
			actionID = "ask"
		}
		if !s.validateExecutionTarget(w, &body) {
			return
		}
		if !s.resolveInvocationAgent(w, r, userID, &body) {
			return
		}
		// AI execution belongs to the authenticated account.
		st := aiInvocationAdmitState{conversationID: strings.TrimSpace(body.ConversationID), spaceID: ""}
		if !s.chooseInvocationModel(w, r, userID, &body, &st) {
			return
		}
		if !s.bindConversationModel(w, r, userID, &body, &st) {
			return
		}
		if !s.applyRequestedModelOverride(w, r, &body, &st) {
			return
		}
		if !s.admitInvocationExecution(w, r, userID, &body, &st) {
			return
		}
		if !s.createInvocationConversation(w, r, userID, &body, &st) {
			return
		}
		if !s.saveConversationModelOverride(w, r, userID, &body, &st) {
			return
		}
		if !checkInvocationModelAvailable(w, r, &body, &st) {
			return
		}
		if !s.checkInvocationSurface(w, r, userID, &body, actionID, &st) {
			return
		}
		if !s.admitInvocationCollaboration(w, r, userID, &body, &st) {
			return
		}
		stored, created, now, ok := s.storeInvocationRecord(w, r, userID, &body, &st, scheduled)
		if !ok {
			return
		}
		if created && s.providerSettings != nil && !s.freezeInvocationRoutes(w, r, userID, stored.ID, &st) {
			return
		}
		if scheduled != nil {
			if err := s.database.BindWorkflowScheduleRun(r.Context(), *scheduled.schedule, st.conversationID, stored.ID); err != nil {
				WriteAIError(w, err)
				return
			}
			admittedRun = true
		}
		record, err := s.invocations.restoreDurable(r.Context(), stored)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load invocation stream"})
			return
		}
		if scheduled != nil && created {
			if notice := scheduled.lateNotice(now); notice != "" {
				s.invocations.append(stored.ID, aiInvocationEvent{Type: "assistant.status", Text: notice, Phase: "schedule_late"})
			}
		}
		if !created {
			writeAIInvocationCreated(w, record)
			return
		}
		titleNewConversation(s.database, s.runtime, userID, st.conversationID, body)
		s.recordInvocationOnboarding(r.Context(), userID, body)
		if st.modelFallbackNotice {
			s.invocations.append(stored.ID, aiInvocationEvent{Type: "assistant.status", Text: "The previous model retired, so Misty switched this conversation to the frontier default.", Phase: "model_fallback"})
		}
		writeAIInvocationCreated(w, record)
	}
}

func writeAIInvocationCreated(w http.ResponseWriter, record *aiInvocationRecord) {
	writeJSON(w, http.StatusAccepted, map[string]any{
		"invocationId":   record.ID,
		"conversationId": record.ConversationID,
		"state":          record.State,
		"eventsUrl":      "/ai/invocations/" + record.ID + "/events",
	})
}

func (s *AIService) CancelInvocation() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		stored, err := s.database.AIInvocationByID(r.Context(), userID, chi.URLParam(r, "invocationID"))
		if err != nil {
			http.Error(w, "invocation not found", http.StatusNotFound)
			return
		}

		if _, err := s.invocations.restoreDurable(r.Context(), *stored); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load invocation stream"})
			return
		}
		if err := s.database.CancelMistyInvocationChildren(r.Context(), userID, stored.ID); err != nil {
			writeSpaceError(w, err)
			return
		}
		if s.providerSettings != nil {
			// Sub-agents stop with the run that started them.
			s.providerSettings.cancelDelegatedChildren(r.Context(), userID, stored.ID)
		}
		if !aiInvocationTerminal(stored.State) && stored.AgentRunID != "" {
			if run, cancelErr := s.database.CancelPersonalAgentTaskRunForOwner(r.Context(), userID, stored.AgentRunID); cancelErr == nil && run.RuntimeRunID != "" {
				_ = s.agentRuntime.Cancel(r.Context(), run.RuntimeRunID, run.ID)
			}
		} else if !aiInvocationTerminal(stored.State) && stored.RuntimeRunID != "" {
			_ = s.agentRuntime.Cancel(r.Context(), stored.RuntimeRunID, stored.ID)
		}
		current, err := s.database.AIInvocationByID(r.Context(), userID, stored.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Misty could not load the stop result."})
			return
		}
		_, _ = s.invocations.restoreDurable(r.Context(), *current)
		state := current.State
		_ = s.database.SetAgentQuestionState(r.Context(), userID, "", stored.ID, "canceled")
		writeJSON(w, http.StatusAccepted, map[string]any{"state": state, "runtime_cancel_pending": !aiInvocationTerminal(stored.State) && stored.RuntimeRunID != "", "message": "Stop requested. Previously completed or uncertain actions remain in history."})
	}
}
