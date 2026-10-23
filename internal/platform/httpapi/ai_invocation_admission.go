package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/platform/db"
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
	if err := DecodeAIJSONWithLimit(w, r, body, 24<<20); err != nil {
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

// admitInvocationExecution checks that local execution is allowed, that the
// device contexts are valid and that the agent holds grants for them, and
// that an attached project is allowed to code.
func (s *AIService) admitInvocationExecution(w http.ResponseWriter, r *http.Request, userID string, body *aiInvocationInput, st *aiInvocationAdmitState) bool {
	// Attaching a project starts coding: Pro and above, from the Misty app.
	for _, deviceContext := range body.DeviceContexts {
		if strings.TrimSpace(deviceContext.Kind) == "project_folder" && !requireCoding(w, r, s.database, userID, "invocation.project") {
			return false
		}
	}
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
			WriteAIError(w, err)
			return false
		}
		_ = s.database.RenameAgentSession(r.Context(), userID, st.conversationID, cleanMistyTitle(body.Prompt))
		_ = s.database.UpdateMistyConversationModel(r.Context(), userID, st.conversationID, st.modelID, st.reasoning, agent.FrontierModelCatalogVersion)
	}
	if st.conversationID == "" && body.Mode == "companion" {
		st.conversationID, err = s.database.CreatePersonalAgentConversation(r.Context(), userID, st.spaceID, body.AgentID)
		if err != nil {
			WriteAIError(w, err)
			return false
		}
		_ = s.database.RenameAgentSession(r.Context(), userID, st.conversationID, cleanMistyTitle(body.Prompt))
		_ = s.database.UpdateMistyConversationModel(r.Context(), userID, st.conversationID, st.modelID, st.reasoning, agent.FrontierModelCatalogVersion)
	}
	return true
}

// checkInvocationSurface confirms the surface can run the action with the
// chosen model and binds a companion conversation to its surface.
func (s *AIService) checkInvocationSurface(w http.ResponseWriter, r *http.Request, userID string, body *aiInvocationInput, actionID string, st *aiInvocationAdmitState) bool {
	available, err := s.database.AIActionAvailable(r.Context(), userID, body.SurfaceID, actionID, st.modelID)
	if err != nil {
		WriteAIError(w, err)
		return false
	}
	if !available {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "ai_surface_unavailable", "message": "Misty is temporarily unavailable for this action."})
		return false
	}
	if body.Mode == "companion" && st.conversationID != "" {
		if err := s.database.BindAskSurfaceConversation(r.Context(), userID, st.conversationID, st.spaceID, st.modelID, body.SurfaceID, firstAIContextHref(body.Context), aiInvocationPrivacyBoundary(body.Context)); err != nil {
			WriteAIError(w, err)
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
		WriteAIError(w, err)
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
		DispatchRuntime: true, ModelTurnLimit: evalTurnLimit(body.EvalTurnLimit, planTurnLimit(r.Context(), s.database, userID)), ExecutionLimitMS: evalExecutionLimitMS(body.EvalTurnLimit),
		ID: "invocation_" + uuid.NewString(), UserID: userID, SpaceID: st.spaceID, ConversationID: st.conversationID,
		SurfaceID: body.SurfaceID, Mode: body.Mode, Trigger: recordTrigger, State: "queued",
		IdempotencyKey: body.IdempotencyKey, RequestPayload: requestPayload, ExpiresAt: now.Add(aiInvocationTTL),
	})
	if err != nil {
		WriteAIError(w, err)
		return db.AIInvocationRecord{}, false, now, false
	}
	return stored, created, now, true
}
