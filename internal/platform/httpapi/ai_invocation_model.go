package api

import (
	"net/http"
	"strings"

	"github.com/kannachi323/misty/server/internal/agent"
)

// chooseInvocationModel picks the account's model, or the companion's choice,
// and the reasoning setting for the run.
func (s *AIService) chooseInvocationModel(w http.ResponseWriter, r *http.Request, userID string, body *aiInvocationInput, st *aiInvocationAdmitState) bool {
	st.modelID = agent.FrontierDefaultModelID()
	st.reasoning = agent.ManagedReasoning(body.ThinkingMode, body.ReasoningEffort)
	st.modelOverrides = map[string]string{}
	// Everyone runs on the house model and effort (agent.HouseModel). Only a development
	// stack with MISTY_ALLOW_MODEL_OVERRIDE lets an account or a request choose, for evals.
	if !agent.ClientModelChoice() {
		return true
	}
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
	if override := bound.ModelOverride; override != "" && agent.ClientModelChoice() {
		if !agent.FreeEvalModel(override) && agent.SenseModelRetired(r.Context(), "thinking", override) {
			_ = s.database.SetMistyConversationModelOverride(r.Context(), userID, st.conversationID, "")
			st.modelFallbackNotice = true
		} else {
			st.modelID = override
			st.modelOverrides["agent"] = override
		}
	}
	if bound.ModelID != st.modelID || bound.ReasoningEffort != st.reasoning {
		if err := s.database.UpdateMistyConversationModel(r.Context(), userID, st.conversationID, st.modelID, st.reasoning, agent.FrontierModelCatalogVersion); err != nil {
			WriteAIError(w, err)
			return false
		}
	}
	return true
}

// applyRequestedModelOverride pins the model the request asked for, if any.
func (s *AIService) applyRequestedModelOverride(w http.ResponseWriter, r *http.Request, body *aiInvocationInput, st *aiInvocationAdmitState) bool {
	if requested := strings.TrimSpace(body.ModelOverride); requested != "" && agent.ClientModelChoice() {
		if !agent.FreeEvalModel(requested) && !agent.SenseModelAvailable(r.Context(), "thinking", requested) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": "invalid_model", "message": "Choose an available model."})
			return false
		}
		body.ModelOverride = requested
		st.modelID = requested
		st.modelOverrides["agent"] = requested
	}
	return true
}

// saveConversationModelOverride stores the request's model override on its conversation.
func (s *AIService) saveConversationModelOverride(w http.ResponseWriter, r *http.Request, userID string, body *aiInvocationInput, st *aiInvocationAdmitState) bool {
	if body.ModelOverride != "" && st.conversationID != "" && agent.ClientModelChoice() {
		if err := s.database.SetMistyConversationModelOverride(r.Context(), userID, st.conversationID, body.ModelOverride); err != nil {
			WriteAIError(w, err)
			return false
		}
	}
	return true
}

// checkInvocationModelAvailable confirms the chosen model and reasoning are
// offered, then applies the companion's admission reasoning rule.
func checkInvocationModelAvailable(w http.ResponseWriter, r *http.Request, body *aiInvocationInput, st *aiInvocationAdmitState) bool {
	if !st.accountModel && len(st.modelOverrides) == 0 && !agent.FrontierModelAvailable(r.Context(), st.modelID) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "model_unavailable", "message": "Misty’s model is temporarily unavailable. Please try again."})
		return false
	}
	return true
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
