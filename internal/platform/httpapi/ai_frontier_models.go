package api

import (
	"net/http"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
)

func (s *AIService) FrontierModels() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.requireUser(w, r); !ok {
			return
		}
		models, err := serveragent.FrontierGatewayModels(r.Context())
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"code": "model_catalog_unavailable", "message": "Misty's model catalog is temporarily unavailable.",
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"catalog_version":  serveragent.FrontierModelCatalogVersion,
			"default_model_id": serveragent.FrontierDefaultModelID(),
			"models":           models,
		})
	}
}
