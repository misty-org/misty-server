package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

const defaultADKAppName = "misty-ai"

type ADKGeminiProviderConfig struct {
	APIKey   string
	AuthMode string
	Model    string
	Project  string
	Location string
}

type ADKGeminiProvider struct {
	apiKey   string
	authMode string
	model    string
	project  string
	location string

	once   sync.Once
	runner *runner.Runner
	err    error
}

func NewADKGeminiProvider(config ADKGeminiProviderConfig) *ADKGeminiProvider {
	model := strings.TrimSpace(config.Model)
	if model == "" {
		model = defaultGeminiModel
	}
	return &ADKGeminiProvider{
		apiKey:   strings.TrimSpace(config.APIKey),
		authMode: normalizeGeminiAuthMode(config.AuthMode),
		model:    model,
		project:  strings.TrimSpace(config.Project),
		location: strings.TrimSpace(config.Location),
	}
}

func (p *ADKGeminiProvider) ProviderName() string {
	return ProviderGemini
}

func (p *ADKGeminiProvider) ModelName() string {
	return p.model
}

func (p *ADKGeminiProvider) Next(request ModelRequest) (ModelResponse, error) {
	r, err := p.ensureRunner(context.Background())
	if err != nil {
		return ModelResponse{}, err
	}
	content := genai.NewContentFromText(buildAgentPrompt(request), genai.RoleUser)
	var candidates []string
	var partialText strings.Builder
	var allText strings.Builder
	var usage ModelUsage
	for event, err := range r.Run(context.Background(), request.UserID, request.SessionID, content, agent.RunConfig{StreamingMode: agent.StreamingModeNone}) {
		if err != nil {
			return ModelResponse{}, err
		}
		if event == nil {
			continue
		}
		if event.ErrorMessage != "" {
			return ModelResponse{}, fmt.Errorf("gemini ADK request failed: %s", event.ErrorMessage)
		}
		if metadata := event.UsageMetadata; metadata != nil {
			usage = ModelUsage{
				InputTokens:       int64(metadata.PromptTokenCount),
				CachedInputTokens: int64(metadata.CachedContentTokenCount),
				OutputTokens:      int64(metadata.CandidatesTokenCount + metadata.ThoughtsTokenCount),
				ReasoningTokens:   int64(metadata.ThoughtsTokenCount),
			}
		}
		if text := eventText(event.Content); strings.TrimSpace(text) != "" {
			allText.WriteString(text)
			if event.Partial {
				partialText.WriteString(text)
				continue
			}
			if event.IsFinalResponse() {
				candidates = append([]string{text}, candidates...)
			} else {
				candidates = append(candidates, text)
			}
		}
	}
	if strings.TrimSpace(partialText.String()) != "" {
		candidates = append(candidates, partialText.String())
	}
	if strings.TrimSpace(allText.String()) != "" {
		candidates = append(candidates, allText.String())
	}
	if len(candidates) == 0 {
		return ModelResponse{}, fmt.Errorf("gemini ADK response did not contain text")
	}
	response, err := TestingParseFirstProviderJSONResponse(candidates)
	if err != nil {
		return ModelResponse{}, err
	}
	response.Usage = usage
	return response, nil
}

func (p *ADKGeminiProvider) ensureRunner(ctx context.Context) (*runner.Runner, error) {
	p.once.Do(func() {
		p.runner, p.err = p.newRunner(ctx)
	})
	return p.runner, p.err
}

func (p *ADKGeminiProvider) newRunner(ctx context.Context) (*runner.Runner, error) {
	model, err := gemini.NewModel(ctx, p.model, p.clientConfig())
	if err != nil {
		return nil, err
	}
	temperature := float32(0.2)
	maxOutputTokens := int32(MaxModelOutputTokens)
	rootAgent, err := llmagent.New(llmagent.Config{
		Name:            "misty_file_agent",
		Description:     "Plans safe local file organization operations for Misty Desktop.",
		Model:           model,
		Instruction:     mistyAgentInstruction(),
		IncludeContents: llmagent.IncludeContentsNone,
		GenerateContentConfig: &genai.GenerateContentConfig{
			Temperature:     &temperature,
			MaxOutputTokens: maxOutputTokens,
		},
		OutputSchema: agentResponseGenAISchema(),
	})
	if err != nil {
		return nil, err
	}
	return runner.New(runner.Config{
		AppName:           defaultADKAppName,
		Agent:             rootAgent,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
}

func (p *ADKGeminiProvider) clientConfig() *genai.ClientConfig {
	config := &genai.ClientConfig{}
	if p.authMode != geminiAuthADC && p.apiKey != "" {
		config.APIKey = p.apiKey
		return config
	}
	config.Backend = genai.BackendVertexAI
	config.Project = p.project
	config.Location = p.location
	return config
}

func eventText(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range content.Parts {
		if part != nil && !part.Thought && part.Text != "" {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

func TestingParseFirstProviderJSONResponse(candidates []string) (ModelResponse, error) {
	var lastErr error
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		response, err := parseProviderJSONResponse(candidate)
		if err == nil {
			return response, nil
		}
		lastErr = err
		if extracted := extractFirstJSONObject(candidate); extracted != "" && extracted != candidate {
			response, err := parseProviderJSONResponse(extracted)
			if err == nil {
				return response, nil
			}
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("model returned an empty response")
	}
	log.Printf("Gemini ADK returned unparsable agent JSON: %s", redactForLog(candidates[len(candidates)-1], 700))
	return ModelResponse{}, lastErr
}

func extractFirstJSONObject(value string) string {
	start := strings.Index(value, "{")
	if start < 0 {
		return ""
	}
	inString := false
	escaped := false
	depth := 0
	for index := start; index < len(value); index++ {
		character := value[index]
		if escaped {
			escaped = false
			continue
		}
		if character == '\\' && inString {
			escaped = true
			continue
		}
		if character == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch character {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return strings.TrimSpace(value[start : index+1])
			}
		}
	}
	return ""
}

func redactForLog(value string, limit int) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\n", "\\n"))
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}
