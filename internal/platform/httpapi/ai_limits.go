package api

import (
	"net/http"

	agent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const TestingMaxAIJSONBodyBytes = 2 << 20

type AIService struct {
	database *db.Database
	runtime  *agent.Service
	guard    *AIRequestGuard
}

func NewAIService(database *db.Database, runtime *agent.Service) *AIService {
	return &AIService{database: database, runtime: runtime, guard: NewAIRequestGuard()}
}

func (s *AIService) Status() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		tier, err := s.agentTierForUser(userID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"configured": s.runtime.AgentConfigured(tier),
			"provider":   "misty",
			"model":      agent.InitialSelectedModelID,
			"model_name": agent.InitialSelectedModelName,
			"capabilities": map[string]bool{
				"space_conversation_agents": true,
				"asynchronous_runs":         true,
			},
		})
	}
}
