package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/kannachi323/misty/server/internal/modelruntime"
	"github.com/kannachi323/misty/server/internal/platform/config"
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

// WellFormedModelID reports whether id is "<provider>/<model>". Any provider
// is accepted: the runtime decides whether it can run it.
func WellFormedModelID(id string) bool {
	id = strings.TrimSpace(id)
	provider, model, ok := strings.Cut(id, "/")
	return ok && provider != "" && model != "" && len(id) <= 300 && !strings.ContainsAny(id, " \t\r\n")
}

// GatewayModelAvailable reports whether a model ID can be asked for. Models
// are not checked against any catalog: a well-formed ID is accepted.
func GatewayModelAvailable(_ context.Context, modelID string) bool {
	return WellFormedModelID(modelID)
}

// GatewayModelSupportsReasoning reports whether a run may send a reasoning
// effort with the model. The runtime drops efforts a provider doesn't take.
func GatewayModelSupportsReasoning(_ context.Context, modelID string) bool {
	return WellFormedModelID(modelID)
}

func NewGatewayProviderForModelWithReasoning(models *modelruntime.Client, modelID, reasoningEffort string) (ModelProvider, error) {
	modelID = strings.TrimSpace(modelID)
	if !WellFormedModelID(modelID) || len(modelID) > 200 {
		return nil, errors.New("invalid model")
	}
	if !models.Enabled() {
		return nil, modelruntime.ErrUnavailable
	}
	return NewRuntimeProvider(models, modelruntime.Instance(), modelID, modelProviderName(modelID), reasoningEffort), nil
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
