package agent

import (
	"context"
	"strings"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

const (
	FrontierModelCatalogVersion = "misty-gpt-v2"
	DefaultFrontierModelID      = "openai/gpt-6-astra"
)

// FrontierGatewayModel is a model the server offers by default.
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

// FrontierGatewayModels lists the server's default model. It reads no
// catalog: accounts bring their own models, and any provider/model ID runs.
func FrontierGatewayModels(_ context.Context) ([]FrontierGatewayModel, error) {
	frontier := []FrontierGatewayModel{}
	for _, id := range configuredFrontierModelIDs() {
		name := modelDisplayName(id)
		for _, model := range configuredGatewayModels() {
			if model.ID == id && model.Name != "" {
				name = model.Name
			}
		}
		providerID, providerName := frontierProvider(id)
		frontier = append(frontier, FrontierGatewayModel{
			ID: id, Name: name, ProviderID: providerID, ProviderName: providerName,
			Capabilities: []string{"chat", "tools", "vision"}, ReasoningLevels: []string{"high", "xhigh"},
		})
	}
	return frontier, nil
}

// FrontierModelAvailable reports whether a model ID may run: any well-formed
// provider/model ID. No catalog is asked.
func FrontierModelAvailable(_ context.Context, modelID string) bool {
	return FreeEvalModel(modelID) || WellFormedModelID(modelID)
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
