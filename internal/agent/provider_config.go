package agent

import (
	"net/http"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/modelruntime"
	"github.com/kannachi323/misty/server/internal/platform/config"
)

const (
	ProviderMock = "mock"
	// ProviderVercelAI labels a call on the operator's own keys.
	ProviderVercelAI = "vercel_ai_gateway"

	DefaultVercelAIBaseURL      = "https://ai-gateway.vercel.sh/v1"
	DefaultAgentLowGatewayModel = "google/gemini-2.5-flash-lite"
	DefaultAgentMedGatewayModel = "google/gemini-2.5-flash"
)

// NewAgentProvider sends Go's own small calls to the fast model on the
// operator's keys through the agent runtime. Without a runtime there is no model, so
// the mock answers instead.
func NewAgentProvider(models *modelruntime.Client) ModelProvider {
	if !models.Enabled() {
		return MockProvider{}
	}
	return NewRuntimeProvider(models, modelruntime.Instance(), FastModel(), ProviderVercelAI, "")
}

// FastModel is the cheap model for titles, routing and other small calls.
// MISTY_AI_LOW_MODEL overrides it (evals point it at a free model).
func FastModel() string {
	return envOrDefault("MISTY_AI_LOW_MODEL", DefaultAgentLowGatewayModel)
}

func defaultHTTPClient() *http.Client {
	return noRedirectHTTPClient(&http.Client{Timeout: 45 * time.Second})
}

func noRedirectHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	cloned := *client
	cloned.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &cloned
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(config.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(config.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
