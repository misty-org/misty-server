package agent

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/kannachi323/misty/server/internal/modelruntime"
)

// RuntimeProvider sends one agent turn to the agent runtime, which calls the
// model with the AI SDK. The turn asks for Misty's agent response schema, so
// the reply parses exactly like every other provider's.
type RuntimeProvider struct {
	models       *modelruntime.Client
	route        modelruntime.Route
	model        string
	providerName string
	reasoning    string
}

func NewRuntimeProvider(models *modelruntime.Client, route modelruntime.Route, model, providerName, reasoning string) *RuntimeProvider {
	return &RuntimeProvider{models: models, route: route, model: strings.TrimSpace(model), providerName: providerName, reasoning: runtimeReasoning(reasoning)}
}

// runtimeReasoning keeps the efforts the AI SDK accepts. "max" is an
// OpenAI-only effort above xhigh; Misty's Gateway gets the highest common one.
func runtimeReasoning(effort string) string {
	switch effort = strings.ToLower(strings.TrimSpace(effort)); effort {
	case "none", "minimal", "low", "medium", "high", "xhigh":
		return effort
	case "max":
		return "xhigh"
	}
	return ""
}

func (p *RuntimeProvider) ProviderName() string { return p.providerName }
func (p *RuntimeProvider) ModelName() string    { return p.model }

func (p *RuntimeProvider) Next(request ModelRequest) (ModelResponse, error) {
	return p.NextContext(context.Background(), request)
}

func (p *RuntimeProvider) NextContext(ctx context.Context, request ModelRequest) (ModelResponse, error) {
	if p.model == "" {
		return ModelResponse{}, errors.New("no model is configured for this request")
	}
	prompt, images := buildAgentPromptWithImages(request)
	content := []modelruntime.Part{modelruntime.Text(prompt)}
	for _, image := range images {
		mediaType, data, ok := decodeImageDataURL(image.DataURL)
		if !ok {
			continue
		}
		content = append(content, modelruntime.Text("Image source: "+image.Label), modelruntime.Image(mediaType, data))
	}
	result, err := p.models.Text(ctx, modelruntime.TextRequest{
		Route:           p.route,
		Model:           p.model,
		System:          "Return only JSON that matches the provided schema.",
		Messages:        []modelruntime.Message{{Role: "user", Content: content}},
		MaxOutputTokens: MaxModelOutputTokens,
		Reasoning:       p.reasoning,
		Schema:          &modelruntime.Schema{Name: "misty_agent_response", Schema: TestingAgentResponseJSONSchema()},
	})
	if err != nil {
		return ModelResponse{}, err
	}
	text := result.Text
	if len(result.Object) > 0 && string(result.Object) != "null" {
		text = string(result.Object)
	}
	response, err := parseProviderJSONResponse(text)
	if err != nil {
		return ModelResponse{}, err
	}
	response.Usage = modelUsage(result.Usage)
	return response, nil
}

func modelUsage(usage modelruntime.Usage) ModelUsage {
	return ModelUsage{InputTokens: usage.InputTokens, CachedInputTokens: usage.CachedInputTokens, OutputTokens: usage.OutputTokens, ReasoningTokens: usage.ReasoningTokens}
}

// decodeImageDataURL reads a base64 image data URL produced for the prompt.
func decodeImageDataURL(value string) (string, []byte, bool) {
	header, encoded, found := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
	mediaType, isBase64 := strings.CutSuffix(header, ";base64")
	if !found || !isBase64 || !strings.HasPrefix(mediaType, "image/") {
		return "", nil, false
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	return mediaType, data, err == nil
}
