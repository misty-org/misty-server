package api

import (
	"net/http"
	"time"

	"github.com/misty-org/misty-server/internal/agent"
	"github.com/misty-org/misty-server/internal/platform/db"
	platformmetrics "github.com/misty-org/misty-server/internal/platform/metrics"
)

const MaxAIJSONBodyBytes = 2 << 20

type AIService struct {
	database              *db.Database
	providerSettings      *SpacesService
	runtime               *agent.Service
	invocations           *aiInvocationHub
	streams               *invocationStreams
	metrics               *platformmetrics.Registry
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
		if _, ok := s.requireUser(w, r); !ok {
			return
		}
		// The run's model is the desktop's own; this is the model it is recorded
		// under until the desktop reports it.
		model := agent.FrontierDefaultModelID()
		writeJSON(w, http.StatusOK, map[string]any{
			"configured": s.agentRuntime.Enabled(),
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
