package api

import (
	"net/http"
	"strings"

	"github.com/kannachi323/misty/server/internal/agent"
)

func (s *AIService) FrontierModels() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		model, _, accountModel, selectErr := s.accountAgentModel(r.Context(), user)
		if selectErr != nil {
			writeAIProviderError(w, selectErr)
			return
		}
		if accountModel {
			provider, native, _ := strings.Cut(model, "/")
			writeJSON(w, http.StatusOK, map[string]any{"catalog_version": agent.FrontierModelCatalogVersion, "default_model_id": model, "models": []agent.FrontierGatewayModel{{ID: model, Name: native, ProviderID: provider, ProviderName: provider, Capabilities: []string{"chat", "tools", "vision"}, ReasoningLevels: []string{"default", "none", "low", "medium", "high", "xhigh", "max"}}}})
			return
		}
		models, err := agent.FrontierGatewayModels(r.Context())
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"code": "model_catalog_unavailable", "message": "Misty's model catalog is temporarily unavailable.",
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"catalog_version":  agent.FrontierModelCatalogVersion,
			"default_model_id": agent.FrontierDefaultModelID(),
			"models":           models,
		})
	}
}
