package agent

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

