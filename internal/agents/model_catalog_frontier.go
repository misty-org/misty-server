package agent

import (
	"context"
	"sort"
	"strings"
)

const (
	FrontierModelCatalogVersion = "misty-gpt-v2"
	DefaultFrontierModelID      = "openai/gpt-6-astra"
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

// Model selection is a server release policy. Legacy client model IDs and catalog
// overrides cannot change the model used by a personal agent.
func FrontierDefaultModelID() string { return DefaultFrontierModelID }

func configuredFrontierModelIDs() []string { return []string{FrontierDefaultModelID()} }

// ManagedReasoning migrates legacy settings to the two supported presets.
func ManagedReasoning(mode, legacyEffort string) string {
	if mode == "deep" || (mode == "" && strings.TrimSpace(legacyEffort) == "xhigh") {
		return "xhigh"
	}
	return "high"
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
		levels := []string{"high", "xhigh"}
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
