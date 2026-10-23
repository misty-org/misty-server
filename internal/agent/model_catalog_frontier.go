package agent

import (
	"context"
	"sort"
	"strings"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
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

// Model selection is release policy unless the operator pins a model.
func FrontierDefaultModelID() string {
	if config, err := envconfig.AgentModel(); err == nil && config.Model != "" {
		return config.Model
	}
	return DefaultFrontierModelID
}

func configuredFrontierModelIDs() []string { return []string{FrontierDefaultModelID()} }

// ManagedReasoning is the run's effort. Thinking modes and stored efforts are ignored:
// everyone gets HouseReasoning (see house_route.go).
func ManagedReasoning(mode, legacyEffort string) string {
	return HouseEffort(legacyEffort)
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
	// A free development model (nim/…) is not in the Gateway's catalog, so it is available by its own
	// rule (FreeEvalModel, which only a development stack honors): without this, a request that names
	// no model was refused as "model unavailable" when the house model was a free one.
	if FreeEvalModel(modelID) {
		return true
	}
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
	if name := providerNames[provider]; name != "" {
		return provider, name
	}
	// Unlisted makers read as a name, never an all-caps slug.
	words := strings.Fields(strings.ReplaceAll(provider, "-", " "))
	for i, word := range words {
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return provider, strings.Join(words, " ")
}

// providerNames spells each Gateway maker the way the company writes it,
// never in all caps.
var providerNames = map[string]string{
	"alibaba": "Qwen", "amazon": "Amazon", "anthropic": "Anthropic",
	"arcee-ai": "Arcee", "bytedance": "ByteDance", "callstack": "Callstack",
	"cohere": "Cohere", "deepseek": "DeepSeek", "fireworks": "Fireworks",
	"fish-audio": "Fish Audio", "google": "Google", "inception": "Inception",
	"inclusionai": "InclusionAI", "interfaze": "Interfaze", "meituan": "Meituan",
	"meta": "Meta", "microsoft": "Microsoft", "minimax": "MiniMax",
	"mistral": "Mistral", "mixedbread": "Mixedbread", "moonshotai": "Moonshot AI",
	"nvidia": "Nvidia", "openai": "OpenAI", "openai-compatible": "OpenAI-compatible",
	"perplexity": "Perplexity", "poolside": "Poolside", "quiverai": "QuiverAI",
	"sakana": "Sakana AI", "spacexai": "xAI", "stealth": "Stealth",
	"stepfun": "StepFun", "tencent": "Tencent", "thinkingmachines": "Thinking Machines",
	"xiaomi": "Xiaomi", "zai": "Z.ai",
}
