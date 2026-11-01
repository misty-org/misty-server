package api

import (
	"net/http"
	"strings"

	"github.com/kannachi323/misty/server/internal/agent"
)

// chooseInvocationModel records the run under the model the app named, else
// the default. The desktop's runtime thinks on the device's own models and
// reports them when it starts the run (docs/agents/runtime.md).
func (s *AIService) chooseInvocationModel(w http.ResponseWriter, r *http.Request, userID string, body *aiInvocationInput, st *aiInvocationAdmitState) bool {
	st.modelID = agent.FrontierDefaultModelID()
	if model := strings.TrimSpace(body.ModelID); model != "" {
		st.modelID = model
	}
	st.reasoning = agent.ManagedReasoning(body.ThinkingMode, body.ReasoningEffort)
	return true
}

// bindConversationModel binds an existing conversation to the run's agent and
// records the run's model on it.
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
	if bound.ModelID != st.modelID || bound.ReasoningEffort != st.reasoning {
		if err := s.database.UpdateMistyConversationModel(r.Context(), userID, st.conversationID, st.modelID, st.reasoning, agent.FrontierModelCatalogVersion); err != nil {
			WriteAIError(w, err)
			return false
		}
	}
	return true
}

// freezeInvocationRoutes records the run's models on its new record.
func (s *AIService) freezeInvocationRoutes(w http.ResponseWriter, r *http.Request, userID, invocationID string, st *aiInvocationAdmitState) bool {
	if _, routeErr := s.database.FreezeAIModelRoutes(r.Context(), userID, invocationID, runModelRoutes(st.modelID, st.reasoning)); routeErr != nil {
		writeAIProviderError(w, routeErr)
		return false
	}
	return true
}
