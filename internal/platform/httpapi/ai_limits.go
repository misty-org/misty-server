package api

import (
	"net/http"

	agent "github.com/kannachi323/misty/server/internal/agents"
	platformmetrics "github.com/kannachi323/misty/server/internal/platform/metrics"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const TestingMaxAIJSONBodyBytes = 2 << 20

type AIService struct {
	database    *db.Database
	runtime     *agent.Service
	guard       *AIRequestGuard
	invocations *aiInvocationHub
	metrics     *platformmetrics.Registry
	analyzer    *agent.SmartLibraryAnalyzer
}

func (s *AIService) SetMetrics(registry *platformmetrics.Registry) { s.metrics = registry }
func (s *AIService) SetEmbeddingAnalyzer(analyzer *agent.SmartLibraryAnalyzer) {
	s.analyzer = analyzer
}

func NewAIService(database *db.Database, runtime *agent.Service) *AIService {
	return &AIService{database: database, runtime: runtime, guard: NewAIRequestGuard(), invocations: newAIInvocationHub(database)}
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
