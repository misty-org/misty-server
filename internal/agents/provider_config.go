package agent

import (
	"net/http"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

const (
	ProviderMock       = "mock"
	ProviderOpenAI     = "openai"
	ProviderGemini     = "gemini"
	ProviderGeminiREST = "gemini_rest"
	ProviderVercelAI   = "vercel_ai_gateway"

	defaultOpenAIBaseURL                = "https://api.openai.com/v1"
	defaultOpenAIModel                  = "gpt-5.5"
	defaultGeminiBaseURL                = "https://generativelanguage.googleapis.com/v1beta"
	defaultGeminiModel                  = "gemini-3.5-flash"
	TestingDefaultVercelAIBaseURL       = "https://ai-gateway.vercel.sh/v1"
	TestingDefaultAgentLowGatewayModel  = "google/gemini-2.5-flash-lite"
	TestingDefaultAgentMedGatewayModel  = "google/gemini-2.5-flash"
	TestingDefaultAgentHighGatewayModel = "google/gemini-3.5-flash"
)

func NewProviderFromEnv() ModelProvider {
	return newProviderFromEnv("", "")
}

func NewAgentProviderFromEnv() ModelProvider {
	apiKey := firstEnv("AI_GATEWAY_API_KEY", "VERCEL_OIDC_TOKEN")
	if apiKey == "" {
		return NewAgentProviderRouter(MockProvider{}, MockProvider{}, MockProvider{})
	}
	baseURL := envOrDefault("AI_GATEWAY_BASE_URL", TestingDefaultVercelAIBaseURL)
	provider := func(modelKey, fallback string) ModelProvider {
		return NewOpenAIProvider(OpenAIProviderConfig{
			APIKey:       apiKey,
			BaseURL:      baseURL,
			Model:        envOrDefault(modelKey, fallback),
			ProviderName: ProviderVercelAI,
		})
	}
	return NewAgentProviderRouter(
		provider("MISTY_AI_LOW_MODEL", TestingDefaultAgentLowGatewayModel),
		provider("MISTY_AI_MED_MODEL", TestingDefaultAgentMedGatewayModel),
		provider("MISTY_AI_HIGH_MODEL", TestingDefaultAgentHighGatewayModel),
	)
}

func newProviderFromEnv(providerKey, modelKey string) ModelProvider {
	providerName := ""
	if providerKey != "" {
		providerName = strings.ToLower(strings.TrimSpace(envconfig.Getenv(providerKey)))
	}
	if providerName == "" {
		providerName = strings.ToLower(strings.TrimSpace(envconfig.Getenv("MISTY_AI_PROVIDER")))
	}
	openAIKey := strings.TrimSpace(envconfig.Getenv("OPENAI_API_KEY"))
	geminiKey := firstEnv("GEMINI_API_KEY", "GOOGLE_API_KEY")
	geminiAuthMode := strings.ToLower(strings.TrimSpace(envconfig.Getenv("GEMINI_AUTH_MODE")))
	if providerName == "" {
		switch {
		case openAIKey != "":
			providerName = ProviderOpenAI
		case geminiKey != "" || geminiAuthMode == geminiAuthADC:
			providerName = ProviderGemini
		default:
			providerName = ProviderMock
		}
	}

	switch providerName {
	case ProviderOpenAI:
		if openAIKey == "" {
			return MockProvider{}
		}
		return NewOpenAIProvider(OpenAIProviderConfig{
			APIKey:  openAIKey,
			BaseURL: envOrDefault("OPENAI_BASE_URL", defaultOpenAIBaseURL),
			Model:   routeModel(modelKey, defaultOpenAIModel),
		})
	case ProviderGemini:
		if geminiKey == "" && geminiAuthMode == geminiAuthAPIKey {
			return MockProvider{}
		}
		return NewADKGeminiProvider(ADKGeminiProviderConfig{
			APIKey:   geminiKey,
			AuthMode: envOrDefault("GEMINI_AUTH_MODE", geminiAuthAuto),
			Model:    routeModel(modelKey, defaultGeminiModel),
			Project:  firstEnv("GEMINI_VERTEX_PROJECT", "GOOGLE_CLOUD_PROJECT"),
			Location: firstEnv("GEMINI_VERTEX_LOCATION", "GOOGLE_CLOUD_LOCATION", "GOOGLE_CLOUD_REGION"),
		})
	case ProviderGeminiREST:
		if geminiKey == "" && geminiAuthMode == geminiAuthAPIKey {
			return MockProvider{}
		}
		return NewGeminiProvider(GeminiProviderConfig{
			APIKey:     geminiKey,
			AuthMode:   envOrDefault("GEMINI_AUTH_MODE", geminiAuthAuto),
			BaseURL:    envOrDefault("GEMINI_BASE_URL", defaultGeminiBaseURL),
			Model:      routeModel(modelKey, defaultGeminiModel),
			OAuthScope: envOrDefault("GEMINI_OAUTH_SCOPE", defaultGeminiOAuthScope),
		})
	default:
		return MockProvider{}
	}
}

func routeModel(modelKey, fallback string) string {
	if modelKey != "" {
		if value := strings.TrimSpace(envconfig.Getenv(modelKey)); value != "" {
			return value
		}
	}
	return envOrDefault("MISTY_AI_MODEL", fallback)
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
