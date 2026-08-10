package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type OpenAIProviderConfig struct {
	APIKey          string
	BaseURL         string
	Model           string
	ProviderName    string
	ReasoningEffort string
	Client          *http.Client
}

type OpenAIProvider struct {
	TestingApiKey   string
	TestingBaseURL  string
	model           string
	providerName    string
	reasoningEffort string
	client          *http.Client
}

func NewOpenAIProvider(config OpenAIProviderConfig) *OpenAIProvider {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}
	model := strings.TrimSpace(config.Model)
	if model == "" {
		model = defaultOpenAIModel
	}
	client := config.Client
	if client == nil {
		client = defaultHTTPClient()
	} else {
		client = noRedirectHTTPClient(client)
	}
	return &OpenAIProvider{
		TestingApiKey:   strings.TrimSpace(config.APIKey),
		TestingBaseURL:  baseURL,
		model:           model,
		providerName:    strings.TrimSpace(config.ProviderName),
		reasoningEffort: normalizeReasoningEffort(config.ReasoningEffort),
		client:          client,
	}
}

// normalizeReasoningEffort keeps only the effort levels the Responses API accepts;
// anything else (including empty) yields "" so no reasoning field is sent.
func normalizeReasoningEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(effort))
	default:
		return ""
	}
}

func (p *OpenAIProvider) ProviderName() string {
	if p.providerName != "" {
		return p.providerName
	}
	return ProviderOpenAI
}

func (p *OpenAIProvider) ModelName() string {
	return p.model
}

func (p *OpenAIProvider) Next(request ModelRequest) (ModelResponse, error) {
	return p.NextContext(context.Background(), request)
}

func (p *OpenAIProvider) NextContext(ctx context.Context, request ModelRequest) (ModelResponse, error) {
	if p.TestingApiKey == "" {
		return ModelResponse{}, fmt.Errorf("%s API key is required", p.ProviderName())
	}
	prompt, promptImages := buildAgentPromptWithImages(request)
	userContent := []map[string]any{{"type": "input_text", "text": prompt}}
	for _, image := range promptImages {
		userContent = append(userContent,
			map[string]any{"type": "input_text", "text": "Image source: " + image.Label},
			map[string]any{"type": "input_image", "image_url": image.DataURL},
		)
	}
	body := map[string]any{
		"model": p.model,
		"input": []map[string]any{
			{
				"role": "system",
				"content": []map[string]any{{
					"type": "input_text",
					"text": "Return only JSON that matches the provided schema.",
				}},
			},
			{
				"role":    "user",
				"content": userContent,
			},
		},
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "misty_agent_response",
				"strict": true,
				"schema": TestingAgentResponseJSONSchema(),
			},
		},
		"max_output_tokens": MaxModelOutputTokens,
	}
	if p.reasoningEffort != "" {
		body["reasoning"] = map[string]any{"effort": p.reasoningEffort}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return ModelResponse{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TestingBaseURL+"/responses", bytes.NewReader(payload))
	if err != nil {
		return ModelResponse{}, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+p.TestingApiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpResponse, err := p.client.Do(httpRequest)
	if err != nil {
		return ModelResponse{}, err
	}
	defer httpResponse.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(httpResponse.Body, 4<<20))
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return ModelResponse{}, fmt.Errorf("%s request failed: status %d: %s", p.ProviderName(), httpResponse.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	text, err := extractOpenAIText(responseBody)
	if err != nil {
		return ModelResponse{}, err
	}
	response, err := parseProviderJSONResponse(text)
	if err != nil {
		return ModelResponse{}, err
	}
	response.Usage = TestingExtractOpenAIUsage(responseBody)
	return response, nil
}

func TestingExtractOpenAIUsage(body []byte) ModelUsage {
	var payload struct {
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
			InputDetails struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputDetails struct {
				ReasoningTokens int64 `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return ModelUsage{}
	}
	return ModelUsage{InputTokens: payload.Usage.InputTokens, CachedInputTokens: payload.Usage.InputDetails.CachedTokens,
		OutputTokens: payload.Usage.OutputTokens, ReasoningTokens: payload.Usage.OutputDetails.ReasoningTokens}
}

func extractOpenAIText(body []byte) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("AI provider returned invalid JSON: %w", err)
	}
	if text, ok := payload["output_text"].(string); ok && strings.TrimSpace(text) != "" {
		return text, nil
	}
	if text := findTypedText(payload, "output_text"); text != "" {
		return text, nil
	}
	if text := findTypedText(payload, "text"); text != "" {
		return text, nil
	}
	return "", fmt.Errorf("AI provider response did not contain output text")
}

func findTypedText(value any, textKey string) string {
	switch typed := value.(type) {
	case map[string]any:
		if text, ok := typed[textKey].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
		for _, child := range typed {
			if text := findTypedText(child, textKey); text != "" {
				return text
			}
		}
	case []any:
		for _, child := range typed {
			if text := findTypedText(child, textKey); text != "" {
				return text
			}
		}
	}
	return ""
}
