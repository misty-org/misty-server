// Package aimodels defines the internal model roles. Every model runs on
// Misty's own keys.
package aimodels

import (
	"context"
	"errors"
	"strings"

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

// Resolved is an account's chosen model for a role.
type Resolved struct {
	Model     string
	Reasoning string
}
type Resolver func(context.Context, string, string) (*Resolved, error)

// DirectOpenAI reports whether a model skips the Gateway: OpenAI models use
// the instance OpenAI key when one is set. The agent runtime applies the same
// rule; keep them in step.
func DirectOpenAI(model string) bool {
	return strings.HasPrefix(model, "openai/") && config.OpenAIKey() != ""
}

// UsageProvider names who bills a model call.
func UsageProvider(model string) string {
	if DirectOpenAI(model) {
		return "openai"
	}
	return "ai-gateway"
}
