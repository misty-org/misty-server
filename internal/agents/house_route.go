package agent

import (
	"strings"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

// HouseReasoning is the one reasoning effort every run uses. People never choose
// a model or an effort: everyone gets the same agent.
const HouseReasoning = "low"

// ClientModelChoice reports whether a request may pick its own model or effort.
// Production never allows it. A development stack allows it only when
// MISTY_ALLOW_MODEL_OVERRIDE is set, so evals can compare models.
func ClientModelChoice() bool {
	if !strings.EqualFold(strings.TrimSpace(envconfig.Getenv("MISTY_ENVIRONMENT")), "development") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(envconfig.Getenv("MISTY_ALLOW_MODEL_OVERRIDE"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// FreeEvalModel reports whether id is a model on NVIDIA's free hosted endpoints (`nim/…`),
// which a development stack with ClientModelChoice runs for evals without paid credit
// (docs/plans/free-model-evals.md). These ids are not in the Gateway's catalog, so the
// catalog's availability and retirement checks don't apply to them.
func FreeEvalModel(id string) bool {
	return strings.HasPrefix(strings.TrimSpace(id), "nim/") && ClientModelChoice()
}

// HouseModel is the instance's model, or the requested one where ClientModelChoice allows it.
func HouseModel(requested string) string {
	if requested = strings.TrimSpace(requested); requested != "" && ClientModelChoice() {
		return requested
	}
	return FrontierDefaultModelID()
}

// HouseEffort is HouseReasoning, or the requested effort where ClientModelChoice allows it.
func HouseEffort(requested string) string {
	if requested = strings.ToLower(strings.TrimSpace(requested)); requested != "" && ClientModelChoice() {
		switch requested {
		case "none", "minimal", "low", "medium", "high", "xhigh", "max":
			return requested
		}
	}
	return HouseReasoning
}
