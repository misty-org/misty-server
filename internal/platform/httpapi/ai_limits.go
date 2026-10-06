package api

import (
	"net/http"
	"time"

	agent "github.com/kannachi323/misty/server/internal/agents"
	platformmetrics "github.com/kannachi323/misty/server/internal/platform/metrics"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const TestingMaxAIJSONBodyBytes = 2 << 20

type AIService struct {
	database              *db.Database
	providerSettings      *SpacesService
	runtime               *agent.Service
	invocations           *aiInvocationHub
	streams               *invocationStreams
	metrics               *platformmetrics.Registry
	analyzer              *agent.SmartLibraryAnalyzer
	agentRuntime          AgentRuntimeConfig
	attachmentStore       LibraryObjectStore
	attachmentPresigner   LibraryObjectPresigner
	attachmentUploadTTL   time.Duration
	attachmentDownloadTTL time.Duration
}

func (s *AIService) SetMetrics(registry *platformmetrics.Registry) {
	s.metrics = registry
	if s.streams != nil {
		s.streams.setMetrics(registry)
	}
}
func (s *AIService) SetEmbeddingAnalyzer(analyzer *agent.SmartLibraryAnalyzer) {
	s.analyzer = analyzer
}
func (s *AIService) SetAgentRuntime(config AgentRuntimeConfig) {
	config.database = s.database
	s.agentRuntime = config
}

func (s *AIService) SetAttachmentStore(store LibraryObjectStore) {
	s.attachmentStore = store
	if presigner, ok := store.(LibraryObjectPresigner); ok {
		s.attachmentPresigner = presigner
	}
	ttls := DefaultTransferTTLs()
	s.attachmentUploadTTL = ttls.UploadURLTTL
	s.attachmentDownloadTTL = ttls.DownloadURLTTL
}

// AttachSpacesRuntime shares only the durable invocation event projection with
// the Space control plane. Personal-Agent WorkflowAgent completions can then
// finish the companion invocation that launched them without another model
// runtime or public callback channel.
func (s *AIService) AttachSpacesRuntime(spaces *SpacesService) {
	if spaces != nil {
		spaces.aiInvocations = s.invocations
		s.providerSettings = spaces
	}
}

func NewAIService(database *db.Database, runtime *agent.Service) *AIService {
	return &AIService{database: database, runtime: runtime, invocations: newAIInvocationHub(database), streams: newInvocationStreams(database)}
}

func (s *AIService) Status() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		model := agent.FrontierDefaultModelID()
		selected, _, accountModel, modelErr := s.accountAgentModel(r.Context(), user)
		if accountModel {
			model = selected
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"configured": s.agentRuntime.Enabled() && modelErr == nil,
			"provider":   "misty",
			"model":      model,
			"model_name": model,
			"capabilities": map[string]bool{
				"space_conversation_agents": true,
				"asynchronous_runs":         true,
			},
		})
	}
}
