// Package aimodels defines the internal model roles and how an account's
// model choices resolve. A model runs on the key the account brought from its
// device (byok) or, when the operator configured models, on the server's own
// keys.
package aimodels

import (
	"context"
	"errors"
	"strings"

	"github.com/kannachi323/misty/server/internal/byok"
	"github.com/kannachi323/misty/server/internal/platform/config"
)

var ErrDisabled = errors.New("this AI task is disabled")

// Role is one internal model task. Accounts never see roles.
type Role struct {
	ID        string
	Reasoning bool
}

var Roles = []Role{
	{"agent", true},
	{"vision", true},
	// Reading and acting on web pages; empty means the run's vision model.
	{"browsing", true},
	{"routing", true},
	{"realtime", false},
	{"embedding", false},
	{"transcription", false},
	{"transcription-fallback", false},
	{"media-transcription", false},
	{"media-transcription-fallback", false},
	{"speech", false},
	// Live transcription through the runtime's Soniox relay, and the diarized
	// pass over files and finished captures. Not user-facing: no Gateway
	// catalog model fits them, so they keep Misty's defaults.
	{"live-transcription", false},
	{"diarized-transcription", false},
	// Tidying dictation before it is typed: the fast model.
	{"dictation-cleanup", false},
	// Notes, summaries, action items and chapters from a transcript.
	{"voice-notes", false},
	{"day-summary", false},
}

type Route struct {
	Role      string `json:"role"`
	Model     string `json:"model"`
	Reasoning string `json:"reasoning"`
	Enabled   bool   `json:"enabled"`
}

// Resolved is an account's chosen model for a role. Credential is the
// account's own key for it; nil means the operator's keys run it.
type Resolved struct {
	Model      string
	Reasoning  string
	Credential *byok.Credential
}
type Resolver func(context.Context, string, string) (*Resolved, error)

// DirectOpenAI reports whether the operator's runtime sends a model straight
// to OpenAI: OpenAI models do when the operator set OPENAI_API_KEY. The agent
// runtime applies the same rule; keep them in step.
func DirectOpenAI(model string) bool {
	return strings.HasPrefix(model, "openai/") && config.OpenAIKey() != ""
}

// ClientModelChoice reports whether a request may pick its own model or effort.
// Production never allows it. A development stack allows it only when
// MISTY_ALLOW_MODEL_OVERRIDE is set, so evals can compare models.
func ClientModelChoice() bool {
	if !strings.EqualFold(strings.TrimSpace(config.Getenv("MISTY_ENVIRONMENT")), "development") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(config.Getenv("MISTY_ALLOW_MODEL_OVERRIDE"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// OperatorModels reports whether the server has models of its own: the
// operator set MISTY_AGENT_MODEL (and the runtime's provider keys), or a
// development eval stack picks models per request.
func OperatorModels() bool {
	return strings.TrimSpace(config.Getenv("MISTY_AGENT_MODEL")) != "" || ClientModelChoice()
}
