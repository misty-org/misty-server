package api

import (
	"net/http"

	"github.com/misty-org/misty-server/internal/agent"
	"github.com/misty-org/misty-server/internal/platform/db"
)

func writeAIProviderError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "ai_model_settings_error", "message": "Could not record the run's models. Try again."})
}

// runModelRoutes are the models a run thinks on: the device's agent model,
// which also sees its screenshots, as the desktop reported it.
func runModelRoutes(model, reasoning string) []db.AIModelRoute {
	if model == "" {
		model = agent.FrontierDefaultModelID()
	}
	if reasoning == "" {
		reasoning = agent.HouseReasoning
	}
	return []db.AIModelRoute{{Role: "agent", Model: model, Reasoning: reasoning, Enabled: true}, {Role: "vision", Model: model, Reasoning: reasoning, Enabled: true}}
}

func modelRoute(routes []db.AIModelRoute, role string) db.AIModelRoute {
	for _, r := range routes {
		if r.Role == role {
			return r
		}
	}
	return db.AIModelRoute{Role: role, Enabled: true}
}
