package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	agent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// aiInvocationAdmitState carries what admission decides about the run's
// conversation, model and reasoning as CreateInvocation works through it.
type aiInvocationAdmitState struct {
	conversationID      string
	spaceID             string
	modelID             string
	reasoning           string
	accountModel        bool
	modelOverrides      map[string]string
	modelFallbackNotice bool
}

func decodeAIInvocationBody(w http.ResponseWriter, r *http.Request, body *aiInvocationInput) bool {
	if err := TestingDecodeAIJSONWithLimit(w, r, body, 24<<20); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return false
	}
	return true
}

// admitScheduledInvocation admits a run started from a computer's schedule.
// It returns nil admission and true for ordinary runs.
func (s *AIService) admitScheduledInvocation(w http.ResponseWriter, r *http.Request, userID string, body *aiInvocationInput) (*deviceScheduledRunAdmission, bool) {
	var scheduled *deviceScheduledRunAdmission
	if strings.TrimSpace(body.Trigger) == scheduledRunTrigger || body.ScheduleClaim != "" {
		admitted, err := s.admitDeviceScheduledRun(r.Context(), userID, body)
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"code": "schedule_not_waiting", "message": err.Error()})
			return nil, false
		}
		scheduled = admitted
	}
	return scheduled, true
}

func (s *AIService) validateInvocationRequest(w http.ResponseWriter, r *http.Request, userID string, body *aiInvocationInput) bool {
	if _, err := resolveInvocationMethod(r.Context(), s.database, userID, body); err != nil {
		writeAgentMethodError(w, err)
		return false
	}
	if err := validateAIInvocationInput(body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "invalid_invocation", "message": err.Error()})
		return false
	}
	return true
}

// ensureIdempotencyKey takes the key from the header when one is sent and
// requires that the run has a key either way.
func ensureIdempotencyKey(w http.ResponseWriter, r *http.Request, body *aiInvocationInput) bool {
	if header := strings.TrimSpace(r.Header.Get("Idempotency-Key")); header != "" {
		if body.IdempotencyKey != "" && body.IdempotencyKey != header {
			http.Error(w, "idempotency key mismatch", http.StatusBadRequest)
			return false
		}
		body.IdempotencyKey = header
	}
	if body.IdempotencyKey == "" {
		http.Error(w, "idempotency key is required", http.StatusBadRequest)
		return false
	}
	return true
}

// validateExecutionTarget checks that the agent runtime is configured and
// that the requested execution mode has the window it needs.
func (s *AIService) validateExecutionTarget(w http.ResponseWriter, body *aiInvocationInput) bool {
	if !s.agentRuntime.Enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "agent_runtime_unavailable", "message": "Misty's agent runtime is not configured."})
		return false
	}
	if body.ExecutionMode == "" {
		body.ExecutionMode = "user"
	}
	if body.ExecutionMode != "user" && body.ExecutionMode != "agent" && body.ExecutionMode != "team" {
		http.Error(w, "invalid execution mode", http.StatusBadRequest)
		return false
	}
	if body.ExecutionMode != "user" && strings.TrimSpace(body.WindowLabel) == "" {
		http.Error(w, "execution window required", http.StatusBadRequest)
		return false
	}
	return true
}

// resolveInvocationAgent loads the agent that will run the request, checks it
// is enabled and pins the skills the request names.
func (s *AIService) resolveInvocationAgent(w http.ResponseWriter, r *http.Request, userID string, body *aiInvocationInput) bool {
	var identity *db.AskIdentity
	var identityErr error
	if body.AgentID == "" {
		identity, identityErr = s.database.EnsureAskIdentity(r.Context(), userID, agent.FrontierDefaultModelID())
	} else {
		identity, identityErr = s.database.AskIdentityByID(r.Context(), userID, body.AgentID)
	}
	if identityErr != nil {
		writePersonalAgentError(w, identityErr)
		return false
	}
	if !identity.Enabled {
		http.Error(w, "agent disabled", http.StatusConflict)
		return false
	}
	body.AgentID = identity.ID
	if err := pinInvocationSkills(r.Context(), s.database, userID, body); err != nil {
		writeAgentMethodError(w, err)
		return false
	}
	return true
}

// chooseInvocationModel picks the account's model, or the companion's choice,
// and the reasoning setting for the run.
func (s *AIService) chooseInvocationModel(w http.ResponseWriter, r *http.Request, userID string, body *aiInvocationInput, st *aiInvocationAdmitState) bool {
	st.modelID = agent.FrontierDefaultModelID()
	selectedModel, _, accountModel, selectErr := s.accountAgentModel(r.Context(), userID)
	if selectErr != nil {
		writeAIProviderError(w, selectErr)
		return false
	}
	st.accountModel = accountModel
	if accountModel {
		st.modelID = selectedModel
	}
	if !accountModel && body.Mode == "companion" && body.CompanionModel != "" {
		if !agent.FrontierModelAvailable(r.Context(), body.CompanionModel) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": "invalid_model", "message": "Choose an available model."})
			return false
		}
		st.modelID = body.CompanionModel
	}
	st.reasoning = agent.ManagedReasoning(body.ThinkingMode, body.ReasoningEffort)
	st.modelOverrides = map[string]string{}
	return true
}

// bindConversationModel applies an existing conversation's binding, its
// model override and its reasoning setting to the run.
func (s *AIService) bindConversationModel(w http.ResponseWriter, r *http.Request, userID string, body *aiInvocationInput, st *aiInvocationAdmitState) bool {
	if st.conversationID == "" {
		return true
	}
	bound, boundErr := s.database.AgentConversationIdentity(r.Context(), userID, st.conversationID)
	if boundErr != nil || (bound.AgentID != "" && bound.AgentID != body.AgentID) {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "conversation_context_changed", "message": "Start a new Misty task for this Space."})
		return false
	}
	if bindErr := s.database.BindConversationAgent(r.Context(), userID, st.conversationID, body.AgentID); bindErr != nil {
		writePersonalAgentError(w, bindErr)
		return false
	}
	if body.ThinkingMode == "" && body.ReasoningEffort == "" {
		st.reasoning = agent.ManagedReasoning("", bound.ReasoningEffort)
	}
	// The conversation's own pick beats the account's Thinking choice.
	// A model the Gateway retired falls back with a notice.
	if override := bound.ModelOverride; override != "" {
		if agent.SenseModelRetired(r.Context(), "thinking", override) {
			_ = s.database.SetMistyConversationModelOverride(r.Context(), userID, st.conversationID, "")
			st.modelFallbackNotice = true
		} else {
			st.modelID = override
			st.modelOverrides["agent"] = override
		}
	}
	if bound.ModelID != st.modelID || bound.ReasoningEffort != st.reasoning {
		if err := s.database.UpdateMistyConversationModel(r.Context(), userID, st.conversationID, st.modelID, st.reasoning, agent.FrontierModelCatalogVersion); err != nil {
			TestingWriteAIError(w, err)
			return false
		}
	}
	return true
}

// applyRequestedModelOverride pins the model the request asked for, if any.
func (s *AIService) applyRequestedModelOverride(w http.ResponseWriter, r *http.Request, body *aiInvocationInput, st *aiInvocationAdmitState) bool {
	if requested := strings.TrimSpace(body.ModelOverride); requested != "" {
		if !agent.SenseModelAvailable(r.Context(), "thinking", requested) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": "invalid_model", "message": "Choose an available model."})
			return false
		}
		body.ModelOverride = requested
		st.modelID = requested
		st.modelOverrides["agent"] = requested
	}
	return true
}

// admitInvocationExecution checks that local execution is allowed, that the
// device contexts are valid and that the agent holds grants for them.
func (s *AIService) admitInvocationExecution(w http.ResponseWriter, r *http.Request, userID string, body *aiInvocationInput, st *aiInvocationAdmitState) bool {
	payload, _ := json.Marshal(*body)
	if err := s.database.ValidateNativeAgentExecution(r.Context(), &db.AIInvocationRecord{UserID: userID, SpaceID: st.spaceID, RequestPayload: payload}); err != nil {
		http.Error(w, "Local execution is paused or unavailable", http.StatusConflict)
		return false
	}
	if err := validateAIInvocationDeviceContexts(body.Context, body.DeviceContexts, st.spaceID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "invalid_device_context", "message": err.Error()})
		return false
	}
	if err := verifyDeviceContextGrants(r.Context(), s.database, userID, body.AgentID, body.DeviceContexts); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "device_grant_required", "message": "This device couldn't confirm the request. Make sure it's added to your account and allows agents."})
		return false
	}
	return true
}

// createInvocationConversation starts a new conversation for drawer and
// companion requests that do not already have one.
func (s *AIService) createInvocationConversation(w http.ResponseWriter, r *http.Request, userID string, body *aiInvocationInput, st *aiInvocationAdmitState) bool {
	var err error
	if st.conversationID == "" && (body.Mode == "drawer" || body.Mode == "companion") && body.RequestedArtifactKind == "" {
		st.conversationID, err = s.database.CreatePersonalAgentConversation(r.Context(), userID, st.spaceID, body.AgentID)
		if err != nil {
			TestingWriteAIError(w, err)
			return false
		}
		_ = s.database.RenameAgentSession(r.Context(), userID, st.conversationID, cleanMistyTitle(body.Prompt))
		_ = s.database.UpdateMistyConversationModel(r.Context(), userID, st.conversationID, st.modelID, st.reasoning, agent.FrontierModelCatalogVersion)
	}
	if st.conversationID == "" && body.Mode == "companion" {
		st.conversationID, err = s.database.CreatePersonalAgentConversation(r.Context(), userID, st.spaceID, body.AgentID)
		if err != nil {
			TestingWriteAIError(w, err)
			return false
		}
		_ = s.database.RenameAgentSession(r.Context(), userID, st.conversationID, cleanMistyTitle(body.Prompt))
		_ = s.database.UpdateMistyConversationModel(r.Context(), userID, st.conversationID, st.modelID, st.reasoning, agent.FrontierModelCatalogVersion)
	}
	return true
}

// saveConversationModelOverride stores the request's model override on its conversation.
func (s *AIService) saveConversationModelOverride(w http.ResponseWriter, r *http.Request, userID string, body *aiInvocationInput, st *aiInvocationAdmitState) bool {
	if body.ModelOverride != "" && st.conversationID != "" {
		if err := s.database.SetMistyConversationModelOverride(r.Context(), userID, st.conversationID, body.ModelOverride); err != nil {
			TestingWriteAIError(w, err)
			return false
		}
	}
	return true
}

// checkInvocationModelAvailable confirms the chosen model and reasoning are
// offered, then applies the companion's admission reasoning rule.
func checkInvocationModelAvailable(w http.ResponseWriter, r *http.Request, body *aiInvocationInput, st *aiInvocationAdmitState) bool {
	if !st.accountModel && len(st.modelOverrides) == 0 && (!agent.FrontierModelAvailable(r.Context(), st.modelID) || !agent.FrontierModelReasoningAvailable(r.Context(), st.modelID, st.reasoning)) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "model_unavailable", "message": "Misty’s model is temporarily unavailable. Please try again."})
		return false
	}
	st.reasoning = companionAdmissionReasoning(*body, st.reasoning)
	return true
}

// checkInvocationSurface confirms the surface can run the action with the
// chosen model and binds a companion conversation to its surface.
func (s *AIService) checkInvocationSurface(w http.ResponseWriter, r *http.Request, userID string, body *aiInvocationInput, actionID string, st *aiInvocationAdmitState) bool {
	available, err := s.database.AIActionAvailable(r.Context(), userID, body.SurfaceID, actionID, st.modelID)
	if err != nil {
		TestingWriteAIError(w, err)
		return false
	}
	if !available {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "ai_surface_unavailable", "message": "Misty is temporarily unavailable for this action."})
		return false
	}
	if body.Mode == "companion" && st.conversationID != "" {
		if err := s.database.BindAskSurfaceConversation(r.Context(), userID, st.conversationID, st.spaceID, st.modelID, body.SurfaceID, firstAIContextHref(body.Context), aiInvocationPrivacyBoundary(body.Context)); err != nil {
			TestingWriteAIError(w, err)
			return false
		}
	}
	return true
}

// admitInvocationCollaboration records the run's final conversation, model and
// reasoning on the request, then checks the collaboration mode and attachments.
func (s *AIService) admitInvocationCollaboration(w http.ResponseWriter, r *http.Request, userID string, body *aiInvocationInput, st *aiInvocationAdmitState) bool {
	body.ConversationID = st.conversationID
	body.ModelID = st.modelID
	body.ReasoningEffort = st.reasoning
	if err := admitCollaboration(r.Context(), s.database, userID, body); err != nil {
		TestingWriteAIError(w, err)
		return false
	}
	if err := s.database.ValidateAIConversationAttachments(r.Context(), userID, st.conversationID, body.AttachmentIDs); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "invalid_attachment", "message": err.Error()})
		return false
	}
	return true
}

// storeInvocationRecord writes the queued invocation and returns it with the
// time it was admitted and whether this call created it.
func (s *AIService) storeInvocationRecord(w http.ResponseWriter, r *http.Request, userID string, body *aiInvocationInput, st *aiInvocationAdmitState, scheduled *deviceScheduledRunAdmission) (db.AIInvocationRecord, bool, time.Time, bool) {
	requestPayload, err := json.Marshal(*body)
	if err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return db.AIInvocationRecord{}, false, time.Time{}, false
	}
	now := time.Now().UTC()
	recordTrigger := body.Trigger
	if scheduled != nil {
		// Stored like the server's own scheduled runs, and billed the same way.
		recordTrigger = "schedule"
	}
	stored, created, err := s.database.CreateAIInvocationRecord(r.Context(), db.AIInvocationRecord{
		DispatchRuntime: true, ModelTurnLimit: planTurnLimit(r.Context(), s.database, userID),
		ID: "invocation_" + uuid.NewString(), UserID: userID, SpaceID: st.spaceID, ConversationID: st.conversationID,
		SurfaceID: body.SurfaceID, Mode: body.Mode, Trigger: recordTrigger, State: "queued",
		IdempotencyKey: body.IdempotencyKey, RequestPayload: requestPayload, ExpiresAt: now.Add(aiInvocationTTL),
	})
	if err != nil {
		TestingWriteAIError(w, err)
		return db.AIInvocationRecord{}, false, now, false
	}
	return stored, created, now, true
}

// freezeInvocationRoutes plans the model routes for a newly created run and
// freezes them on the record.
func (s *AIService) freezeInvocationRoutes(w http.ResponseWriter, r *http.Request, userID, invocationID string, st *aiInvocationAdmitState) bool {
	planned, forced := planRoutes(r.Context(), s.database, userID, defaultAIRoutes(st.modelID, st.reasoning))
	if _, routeErr := s.database.FreezeAIModelRoutes(r.Context(), userID, invocationID, planned, st.modelOverrides, forced); routeErr != nil {
		writeAIProviderError(w, routeErr)
		return false
	}
	return true
}
