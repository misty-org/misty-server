package api

import (
	"net/http"
	"strings"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/aimodels"
	"github.com/kannachi323/misty/server/internal/byok"
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
		if c := accountCredentials(r.Context(), user); c != nil {
			model, accountModel = c.Models.Agent, true
		}
		if accountModel {
			provider, native, _ := strings.Cut(model, "/")
			writeJSON(w, http.StatusOK, map[string]any{"catalog_version": agent.FrontierModelCatalogVersion, "default_model_id": model, "models": []agent.FrontierGatewayModel{{ID: model, Name: native, ProviderID: provider, ProviderName: provider, Capabilities: []string{"chat", "tools", "vision"}, ReasoningLevels: []string{"default", "none", "low", "medium", "high", "xhigh", "max"}}}})
			return
		}
		models, err := agent.FrontierGatewayModels(r.Context())
		if err != nil || !aimodels.OperatorModels() {
			// No account model and no operator model: nothing can run.
			writeModelError(w, byok.ErrModelKeyNeeded)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"catalog_version":  agent.FrontierModelCatalogVersion,
			"default_model_id": agent.FrontierDefaultModelID(),
			"models":           models,
		})
	}
}
