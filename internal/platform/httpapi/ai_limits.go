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
	database     *db.Database
	runtime      *agent.Service
	invocations  *aiInvocationHub
	metrics      *platformmetrics.Registry
	analyzer     *agent.SmartLibraryAnalyzer
	agentRuntime AgentRuntimeConfig
	attachmentStore LibraryObjectStore
	attachmentPresigner LibraryObjectPresigner
	attachmentUploadTTL time.Duration
	attachmentDownloadTTL time.Duration
}

func (s *AIService) SetMetrics(registry *platformmetrics.Registry) { s.metrics = registry }
func (s *AIService) SetEmbeddingAnalyzer(analyzer *agent.SmartLibraryAnalyzer) {
	s.analyzer = analyzer
}
func (s *AIService) SetAgentRuntime(config AgentRuntimeConfig) { s.agentRuntime = config }

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
	}
}

func NewAIService(database *db.Database, runtime *agent.Service) *AIService {
	return &AIService{database: database, runtime: runtime, invocations: newAIInvocationHub(database)}
}

func (s *AIService) Status() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"configured": s.agentRuntime.Enabled(),
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
