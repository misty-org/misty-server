package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) AgentInstanceWorkflow() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Enabled       bool            `json:"enabled"`
			TriggerConfig json.RawMessage `json:"trigger_config"`
			Consent       json.RawMessage `json:"consent"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if len(body.TriggerConfig) == 0 {
			body.TriggerConfig = json.RawMessage(`{}`)
		}
		if len(body.Consent) == 0 {
			body.Consent = json.RawMessage(`{}`)
		}
		item, err := s.database.ConfigureInstanceWorkflow(r.Context(), userID, chi.URLParam(r, "instanceID"), chi.URLParam(r, "workflowVersionID"), body.Enabled, body.TriggerConfig, body.Consent)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func (s *SpacesService) AgentInstanceConnections() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Bindings map[string]string `json:"bindings"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := s.database.UpdateAgentInstanceConnections(r.Context(), userID, chi.URLParam(r, "instanceID"), body.Bindings)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func (s *SpacesService) AgentInstanceCapabilities() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Grants []db.AgentCapabilityGrant `json:"grants"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if !canonicalCapabilityGrantsKnown(body.Grants) {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		raw, _ := json.Marshal(body.Grants)
		item, err := s.database.UpdateAgentInstanceCapabilityGrants(r.Context(), userID, chi.URLParam(r, "instanceID"), raw)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func canonicalCapabilityGrantsKnown(grants []db.AgentCapabilityGrant) bool {
	descriptors := canonicalAgentToolboxCatalogDescriptors()
	known := make(map[string]string, len(descriptors))
	for _, descriptor := range descriptors {
		known[descriptor.Name] = descriptor.Risk
	}
	for _, grant := range grants {
		if known[grant.Capability] != grant.Risk {
			return false
		}
	}
	return true
}

func (s *SpacesService) WorkflowRuns() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		items, err := s.database.SpaceWorkflowRuns(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "workflowID"), 100)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"runs": items})
	}
}

func (s *SpacesService) ReplaceAgentWorkflow() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			WorkflowVersionID string `json:"workflow_version_id"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		item, err := s.database.ReplaceAgentWorkflow(r.Context(), userID, chi.URLParam(r, "spaceID"), chi.URLParam(r, "agentID"), body.WorkflowVersionID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}
}

func (s *SpacesService) RunDetail() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		runID := chi.URLParam(r, "runID")
		run, err := s.database.SpaceRun(r.Context(), userID, runID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		actions, _ := s.database.RunActions(r.Context(), userID, runID)
		approvals, _ := s.database.RunApprovals(r.Context(), userID, runID)
		steps, _ := s.database.WorkflowRunSteps(r.Context(), userID, runID)
		writeJSON(w, http.StatusOK, map[string]any{"run": run, "actions": actions, "approvals": approvals, "steps": steps})
	}
}

func (s *SpacesService) RunDecision() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Approved bool `json:"approved"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if existing, existingErr := s.database.SpaceRun(r.Context(), userID, chi.URLParam(r, "runID")); existingErr == nil && existing.OwnerUserID != "" {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		run, err := s.database.DecideRunApproval(r.Context(), userID, chi.URLParam(r, "runID"), body.Approved)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if !body.Approved {
			_ = s.database.FinalizeWorkflowEventClaimsForRun(r.Context(), run.ID, "failed")
			writeJSON(w, http.StatusOK, run)
			return
		}
		prompt := TestingPromptFromRun(run)
		finished, err := s.executeCanonicalAgentRun(r, run, prompt)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if err := s.publishResumedRunResponse(r, userID, finished); err != nil {
			writeSpaceError(w, err)
			return
		}
		if finished.State == "completed" || finished.State == "completed_with_errors" {
			_ = s.database.FinalizeWorkflowEventClaimsForRun(r.Context(), finished.ID, "completed")
		} else if finished.State != "awaiting_approval" {
			_ = s.database.FinalizeWorkflowEventClaimsForRun(r.Context(), finished.ID, "failed")
		}
		writeJSON(w, http.StatusOK, finished)
	}
}

func (s *SpacesService) RunCancel() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if existing, existingErr := s.database.SpaceRun(r.Context(), userID, chi.URLParam(r, "runID")); existingErr == nil && existing.OwnerUserID != "" {
			run, err := s.database.CancelPersonalAgentTaskRunForOwner(r.Context(), userID, existing.ID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			if s.agentRuntime.Enabled() && run.RuntimeRunID != "" {
				_ = s.agentRuntime.Cancel(r.Context(), run.RuntimeRunID, run.ID)
			}
			writeJSON(w, http.StatusOK, run)
			return
		}
		run, err := s.database.CancelSpaceRun(r.Context(), userID, chi.URLParam(r, "runID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if err := s.publishResumedRunResponse(r, userID, run); err != nil {
			writeSpaceError(w, err)
			return
		}
		_ = s.database.FinalizeWorkflowEventClaimsForRun(r.Context(), run.ID, "failed")
		writeJSON(w, http.StatusOK, run)
	}
}

func (s *SpacesService) RunRetry() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		previous, previousErr := s.database.SpaceRun(r.Context(), userID, chi.URLParam(r, "runID"))
		if previousErr != nil {
			writeSpaceError(w, previousErr)
			return
		}
		if previous.OwnerUserID != "" {
			retried, retryErr := s.database.RetryPersonalAgentTaskRunForOwner(r.Context(), userID, previous.ID)
			if retryErr != nil {
				writeSpaceError(w, retryErr)
				return
			}
			writeJSON(w, http.StatusCreated, retried)
			return
		}
		if payload, ok := personalAgentChatRetryPayload(previous); ok {
			triggerID, _ := s.database.SpaceAgentMessageTriggerIDForRun(r.Context(), userID, previous.ID)
			if triggerID != "" {
				_ = s.database.UpdateSpaceAgentMessageTrigger(r.Context(), triggerID, "retrying", previous.ID, "", "")
			}
			_, retriedRunID, retryErr := s.runMentionedAgent(
				r.Context(), userID, previous.SpaceID, payload.ConversationID, previous.AgentID,
				previous.SourceMessageID, previous.TriggerKind, payload.Content, payload.FileNodeIDs,
				payload.AttachmentIDs, payload.LibraryItemIDs,
			)
			if retryErr != nil {
				code, message := spaceRunFailureFromError(retryErr)
				if triggerID != "" {
					_ = s.database.UpdateSpaceAgentMessageTrigger(context.WithoutCancel(r.Context()), triggerID, "failed", retriedRunID, code, message)
				}
				writeSpaceError(w, retryErr)
				return
			}
			retried, retryErr := s.database.SpaceRun(r.Context(), userID, retriedRunID)
			if retryErr != nil {
				writeSpaceError(w, retryErr)
				return
			}
			if triggerID != "" {
				_ = s.database.UpdateSpaceAgentMessageTrigger(r.Context(), triggerID, "completed", retried.ID, "", "")
			}
			writeJSON(w, http.StatusOK, retried)
			return
		}
		if previous.SourceType == "suggestion" {
			batch, item, err := s.database.SpaceActionSuggestionForRun(r.Context(), userID, previous.ID)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			if err := s.database.AuthorizeSuggestionAction(r.Context(), userID, batch.SpaceID, item.SelectedAgentID, item.RequiredCapability, batch.Scope); err != nil {
				writeSpaceError(w, err)
				return
			}
			run, followUp, err := s.executeReviewedSuggestion(r.Context(), userID, batch, *item)
			if err != nil {
				_ = s.database.CompleteSpaceActionSuggestionItem(r.Context(), userID, batch.SpaceID, item.ID, "failed", runID(run), "")
				writeSpaceError(w, err)
				return
			}
			_ = s.database.CompleteSpaceActionSuggestionItem(r.Context(), userID, batch.SpaceID, item.ID, "completed", runID(run), followUpID(followUp))
			writeJSON(w, http.StatusOK, run)
			return
		}
		run, err := s.database.RetrySpaceRun(r.Context(), userID, previous.ID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if run.State == "awaiting_approval" {
			writeJSON(w, http.StatusAccepted, run)
			return
		}
		finished, err := s.executeCanonicalAgentRun(r, run, TestingPromptFromRun(run))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if err := s.publishResumedRunResponse(r, userID, finished); err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, finished)
	}
}

type personalAgentChatRetry struct {
	ConversationID string
	Content        []db.MessageSpan
	FileNodeIDs    []string
	AttachmentIDs  []string
	LibraryItemIDs []string
}

func personalAgentChatRetryPayload(run *db.SpaceRun) (personalAgentChatRetry, bool) {
	if run == nil || run.AgentInstanceID != "" || run.AgentID == "" || run.ResourceKind != "agent" ||
		run.SourceType != "direct" && run.SourceType != "group_mention" {
		return personalAgentChatRetry{}, false
	}
	var input struct {
		Content        []db.MessageSpan `json:"content"`
		FileNodeIDs    []string         `json:"file_node_ids"`
		AttachmentIDs  []string         `json:"attachment_ids"`
		LibraryItemIDs []string         `json:"library_item_ids"`
	}
	if json.Unmarshal(run.Input, &input) != nil || len(input.Content) == 0 {
		return personalAgentChatRetry{}, false
	}
	conversationID := run.ScopeConversationID
	if conversationID == "" && run.ConversationScopeKind == db.ConversationScopePrivate {
		conversationID = run.SourceConversationID
	}
	return personalAgentChatRetry{
		ConversationID: conversationID, Content: input.Content, FileNodeIDs: input.FileNodeIDs,
		AttachmentIDs: input.AttachmentIDs, LibraryItemIDs: input.LibraryItemIDs,
	}, true
}

func (s *SpacesService) publishResumedRunResponse(r *http.Request, userID string, run *db.SpaceRun) error {
	return publishCanonicalRunResponse(r.Context(), s.database, s.agent, userID, run)
}
