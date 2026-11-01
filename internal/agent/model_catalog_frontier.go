package agent

import "strings"

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

// HouseReasoning is the one reasoning effort the server records for a run. The
// desktop runs on the device's own models and reports their effort.
const HouseReasoning = "low"

// FrontierDefaultModelID is the model a run is recorded under until its desktop
// reports the device's model.
func FrontierDefaultModelID() string { return DefaultFrontierModelID }

// ManagedReasoning is the run's effort. Thinking modes and stored efforts are ignored:
// everyone gets HouseReasoning.
func ManagedReasoning(mode, legacyEffort string) string {
	return HouseReasoning
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
