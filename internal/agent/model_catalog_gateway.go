package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/misty-org/misty-server/internal/platform/config"
)

const (
	InitialSelectedModelID   = DefaultFrontierModelID
	InitialSelectedModelName = "GPT-6 Astra"
)

var ErrModelUnavailable = errors.New("agent model unavailable")

type GatewayModel struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
	// ContextWindow is the model's input limit in tokens, when the operator's
	// catalog (MISTY_AI_MODEL_CATALOG_JSON) says.
	ContextWindow int `json:"context_window,omitempty"`
}

// DefaultContextWindow is assumed for models whose window is unknown, such as
// an account's own models. It is deliberately conservative.
const DefaultContextWindow = 128_000

// ModelContextWindow is the context window runs compact against: the
// operator's catalog entry for the model, else DefaultContextWindow. It never
// asks a provider.
func ModelContextWindow(_ context.Context, modelID string) int {
	for _, model := range configuredGatewayModels() {
		if model.ID == strings.TrimSpace(modelID) && model.ContextWindow > 0 {
			return model.ContextWindow
		}
	}
	return DefaultContextWindow
}

// configuredGatewayModels is the operator's optional model list
// (MISTY_AI_MODEL_CATALOG_JSON), used only for names and context windows.
func configuredGatewayModels() []GatewayModel {
	var configured []GatewayModel
	if json.Unmarshal([]byte(strings.TrimSpace(config.Getenv("MISTY_AI_MODEL_CATALOG_JSON"))), &configured) == nil {
		return FilterChatModels(configured)
	}
	return nil
}
