package agent

import (
	"net/http"
	"strings"
	"time"

	"github.com/kannachi323/misty/server/internal/modelruntime"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

const (
	ProviderMock = "mock"
	// ProviderVercelAI names Misty's own AI Gateway account in usage records.
	ProviderVercelAI = "vercel_ai_gateway"

	TestingDefaultVercelAIBaseURL       = "https://ai-gateway.vercel.sh/v1"
	TestingDefaultAgentLowGatewayModel  = "google/gemini-2.5-flash-lite"
	TestingDefaultAgentMedGatewayModel  = "google/gemini-2.5-flash"
	TestingDefaultAgentHighGatewayModel = "google/gemini-3.5-flash"
)

// NewAgentProvider routes each tier to Misty's Gateway through the agent
// runtime. Without a runtime there is no model, so the mock answers instead.
func NewAgentProvider(models *modelruntime.Client) ModelProvider {
	if !models.Enabled() {
		return NewAgentProviderRouter(MockProvider{}, MockProvider{}, MockProvider{})
	}
	provider := func(modelKey, fallback string) ModelProvider {
		return NewRuntimeProvider(models, modelruntime.Instance(), envOrDefault(modelKey, fallback), ProviderVercelAI, "")
	}
	return NewAgentProviderRouter(
		provider("MISTY_AI_LOW_MODEL", TestingDefaultAgentLowGatewayModel),
		provider("MISTY_AI_MED_MODEL", TestingDefaultAgentMedGatewayModel),
		provider("MISTY_AI_HIGH_MODEL", TestingDefaultAgentHighGatewayModel),
	)
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
	value := strings.TrimSpace(envconfig.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(envconfig.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
