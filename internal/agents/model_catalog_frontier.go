package agent

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

const (
	FrontierModelCatalogVersion = "misty-frontier-v1"
	DefaultFrontierModelID      = "openai/gpt-5.6-terra"
)

// FrontierGatewayModel is the deliberately small, paid model catalog exposed
// by Misty. The full Gateway catalog remains an implementation detail.
type FrontierGatewayModel struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	ProviderID      string   `json:"provider_id"`
	ProviderName    string   `json:"provider_name"`
	Capabilities    []string `json:"capabilities"`
	ReasoningLevels []string `json:"reasoning_levels"`
}

var defaultFrontierModelIDs = []string{
	"openai/gpt-5.6-sol",
	"openai/gpt-5.6-terra",
	"openai/gpt-5.6-luna",
	"anthropic/claude-opus-5",
	"anthropic/claude-sonnet-5",
	"anthropic/claude-fable-5",
	"google/gemini-3.7-flash",
	"google/gemini-3.1-pro-preview",
	"spacexai/grok-4.5",
	"deepseek/deepseek-v4-flash-vision-exp",
	"alibaba/qwen3.8-max",
}

func FrontierDefaultModelID() string {
	if value := strings.TrimSpace(envconfig.Getenv("MISTY_FRONTIER_DEFAULT_MODEL")); value != "" {
		return value
	}
	return DefaultFrontierModelID
}

func configuredFrontierModelIDs() []string {
	raw := strings.TrimSpace(envconfig.Getenv("MISTY_FRONTIER_MODEL_IDS_JSON"))
	if raw == "" {
		return append([]string(nil), defaultFrontierModelIDs...)
	}
	var values []string
	if json.Unmarshal([]byte(raw), &values) != nil || len(values) == 0 {
		return append([]string(nil), defaultFrontierModelIDs...)
	}
	return values
}

func FrontierGatewayModels(ctx context.Context) ([]FrontierGatewayModel, error) {
	models, err := GatewayModels(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]GatewayModel, len(models))
	for _, model := range models {
		byID[model.ID] = model
	}
	seen := map[string]bool{}
	frontier := []FrontierGatewayModel{}
	for _, id := range configuredFrontierModelIDs() {
		id = strings.TrimSpace(id)
		model, ok := byID[id]
		if !ok || seen[id] || !frontierCapabilities(model.Capabilities) {
			continue
		}
		seen[id] = true
		providerID, providerName := frontierProvider(id)
		levels := []string{"default"}
		if gatewayCapabilitiesInclude(model.Capabilities, "reasoning", "thinking", "reasoning-effort") {
			levels = []string{"default", "low", "medium", "high"}
		}
		frontier = append(frontier, FrontierGatewayModel{
			ID: id, Name: model.Name, ProviderID: providerID, ProviderName: providerName,
			Capabilities: append([]string(nil), model.Capabilities...), ReasoningLevels: levels,
		})
	}
	sort.SliceStable(frontier, func(i, j int) bool {
		if frontier[i].ProviderName == frontier[j].ProviderName {
			return frontier[i].Name < frontier[j].Name
		}
		return frontier[i].ProviderName < frontier[j].ProviderName
	})
	return frontier, nil
}

func FrontierModelAvailable(ctx context.Context, modelID string) bool {
	models, err := FrontierGatewayModels(ctx)
	if err != nil {
		return false
	}
	for _, model := range models {
		if model.ID == strings.TrimSpace(modelID) {
			return true
		}
	}
	return false
}

func FrontierModelReasoningAvailable(ctx context.Context, modelID, effort string) bool {
	effort = strings.TrimSpace(strings.ToLower(effort))
	if effort == "" {
		effort = "default"
	}
	models, err := FrontierGatewayModels(ctx)
	if err != nil {
		return false
	}
	for _, model := range models {
		if model.ID != strings.TrimSpace(modelID) {
			continue
		}
		for _, level := range model.ReasoningLevels {
			if level == effort {
				return true
			}
		}
	}
	return false
}

func frontierCapabilities(capabilities []string) bool {
	return gatewayCapabilitiesInclude(capabilities, "vision") &&
		gatewayCapabilitiesInclude(capabilities, "tools", "tool-use", "tool_calling", "function_calling")
}

func TestingFrontierCapabilities(capabilities []string) bool {
	return frontierCapabilities(capabilities)
}

func gatewayCapabilitiesInclude(capabilities []string, values ...string) bool {
	allowed := map[string]bool{}
	for _, value := range values {
		allowed[strings.ToLower(value)] = true
	}
	for _, capability := range capabilities {
		if allowed[strings.ToLower(strings.TrimSpace(capability))] {
			return true
		}
	}
	return false
}

func frontierProvider(modelID string) (string, string) {
	provider, _, _ := strings.Cut(modelID, "/")
	names := map[string]string{
		"openai": "OpenAI", "anthropic": "Anthropic", "google": "Google",
		"spacexai": "xAI", "deepseek": "DeepSeek", "alibaba": "Qwen",
	}
	if name := names[provider]; name != "" {
		return provider, name
	}
	return provider, strings.ToUpper(provider)
}
