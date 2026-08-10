package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	geminiAuthAuto   = "auto"
	geminiAuthAPIKey = "api_key"
	geminiAuthADC    = "adc"

	defaultGeminiOAuthScope = "https://www.googleapis.com/auth/generative-language"
)

type GeminiProviderConfig struct {
	APIKey      string
	AuthMode    string
	BaseURL     string
	Model       string
	OAuthScope  string
	TokenSource oauth2.TokenSource
	Client      *http.Client
}

type GeminiProvider struct {
	apiKey              string
	authMode            string
	baseURL             string
	model               string
	oauthScope          string
	tokenSource         oauth2.TokenSource
	defaultTokenOnce    sync.Once
	defaultTokenSource  oauth2.TokenSource
	defaultTokenInitErr error
	client              *http.Client
}

func NewGeminiProvider(config GeminiProviderConfig) *GeminiProvider {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultGeminiBaseURL
	}
	model := strings.TrimSpace(config.Model)
	if model == "" {
		model = defaultGeminiModel
	}
	authMode := normalizeGeminiAuthMode(config.AuthMode)
	oauthScope := strings.TrimSpace(config.OAuthScope)
	if oauthScope == "" {
		oauthScope = defaultGeminiOAuthScope
	}
	client := config.Client
	if client == nil {
		client = defaultHTTPClient()
	} else {
		client = noRedirectHTTPClient(client)
	}
	return &GeminiProvider{
		apiKey:      strings.TrimSpace(config.APIKey),
		authMode:    authMode,
		baseURL:     baseURL,
		model:       model,
		oauthScope:  oauthScope,
		tokenSource: config.TokenSource,
		client:      client,
	}
}

func (p *GeminiProvider) ProviderName() string {
	return ProviderGeminiREST
}

func (p *GeminiProvider) ModelName() string {
	return p.model
}

func (p *GeminiProvider) Next(request ModelRequest) (ModelResponse, error) {
	if p.apiKey == "" {
		if p.authMode == geminiAuthAPIKey {
			return ModelResponse{}, fmt.Errorf("GEMINI_API_KEY is required when GEMINI_AUTH_MODE=api_key")
		}
	}
	body := map[string]any{
		"model": p.model,
		"input": buildAgentPrompt(request),
		"generation_config": map[string]any{
			"max_output_tokens": MaxModelOutputTokens,
		},
		"response_format": map[string]any{
			"type":      "text",
			"mime_type": "application/json",
			"schema":    TestingAgentResponseJSONSchema(),
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return ModelResponse{}, err
	}
	httpRequest, err := http.NewRequest(http.MethodPost, p.baseURL+"/interactions", bytes.NewReader(payload))
	if err != nil {
		return ModelResponse{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if err := p.applyAuth(httpRequest); err != nil {
		return ModelResponse{}, err
	}
	httpResponse, err := p.client.Do(httpRequest)
	if err != nil {
		return ModelResponse{}, err
	}
	defer httpResponse.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(httpResponse.Body, 4<<20))
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return ModelResponse{}, fmt.Errorf("gemini request failed: status %d: %s", httpResponse.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	text, err := extractGeminiText(responseBody)
	if err != nil {
		return ModelResponse{}, err
	}
	response, err := parseProviderJSONResponse(text)
	if err != nil {
		return ModelResponse{}, err
	}
	response.Usage = TestingExtractGeminiUsage(responseBody)
	return response, nil
}

func TestingExtractGeminiUsage(body []byte) ModelUsage {
	var payload struct {
		InteractionUsage struct {
			InputTokens   int64 `json:"total_input_tokens"`
			CachedTokens  int64 `json:"total_cached_tokens"`
			OutputTokens  int64 `json:"total_output_tokens"`
			ThoughtTokens int64 `json:"total_thought_tokens"`
		} `json:"usage"`
		LegacyUsage struct {
			PromptTokens     int64 `json:"promptTokenCount"`
			CachedTokens     int64 `json:"cachedContentTokenCount"`
			CandidatesTokens int64 `json:"candidatesTokenCount"`
			ThoughtsTokens   int64 `json:"thoughtsTokenCount"`
		} `json:"usageMetadata"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return ModelUsage{}
	}
	if payload.InteractionUsage.InputTokens != 0 || payload.InteractionUsage.OutputTokens != 0 || payload.InteractionUsage.ThoughtTokens != 0 {
		return ModelUsage{InputTokens: payload.InteractionUsage.InputTokens, CachedInputTokens: payload.InteractionUsage.CachedTokens,
			OutputTokens: payload.InteractionUsage.OutputTokens + payload.InteractionUsage.ThoughtTokens, ReasoningTokens: payload.InteractionUsage.ThoughtTokens}
	}
	return ModelUsage{InputTokens: payload.LegacyUsage.PromptTokens, CachedInputTokens: payload.LegacyUsage.CachedTokens,
		OutputTokens: payload.LegacyUsage.CandidatesTokens + payload.LegacyUsage.ThoughtsTokens, ReasoningTokens: payload.LegacyUsage.ThoughtsTokens}
}

func (p *GeminiProvider) applyAuth(request *http.Request) error {
	if p.authMode != geminiAuthADC && p.apiKey != "" {
		request.Header.Set("X-Goog-Api-Key", p.apiKey)
		return nil
	}
	source, err := p.adcTokenSource(request.Context())
	if err != nil {
		return fmt.Errorf("gemini ADC auth failed: %w", err)
	}
	token, err := source.Token()
	if err != nil {
		return fmt.Errorf("gemini ADC token failed: %w", err)
	}
	if !token.Valid() || strings.TrimSpace(token.AccessToken) == "" {
		return fmt.Errorf("gemini ADC token is invalid")
	}
	request.Header.Set("Authorization", "Bearer "+token.AccessToken)
	return nil
}

func (p *GeminiProvider) adcTokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	if p.tokenSource != nil {
		return p.tokenSource, nil
	}
	p.defaultTokenOnce.Do(func() {
		p.defaultTokenSource, p.defaultTokenInitErr = google.DefaultTokenSource(ctx, p.oauthScope)
	})
	return p.defaultTokenSource, p.defaultTokenInitErr
}

func normalizeGeminiAuthMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case geminiAuthAPIKey, "apikey", "api-key", "key":
		return geminiAuthAPIKey
	case geminiAuthADC, "application_default_credentials", "application-default-credentials", "oauth":
		return geminiAuthADC
	default:
		return geminiAuthAuto
	}
}

func extractGeminiText(body []byte) (string, error) {
	var generic map[string]any
	if err := json.Unmarshal(body, &generic); err != nil {
		return "", fmt.Errorf("gemini returned invalid JSON: %w", err)
	}
	if text, ok := generic["output_text"].(string); ok && strings.TrimSpace(text) != "" {
		return text, nil
	}
	if text := findTypedText(generic, "output_text"); text != "" {
		return text, nil
	}

	var payload struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	_ = json.Unmarshal(body, &payload)
	for _, candidate := range payload.Candidates {
		for _, part := range candidate.Content.Parts {
			if strings.TrimSpace(part.Text) != "" {
				return part.Text, nil
			}
		}
	}
	return "", fmt.Errorf("gemini response did not contain text")
}
