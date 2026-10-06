// Package aimodels defines account-owned model choices. Every model runs on
// Misty's own keys: an account only chooses which model each sense uses.
package aimodels

import (
	"context"
	"errors"
	"strings"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

var ErrDisabled = errors.New("this AI task is disabled")

// Role is one internal model task. Accounts never see roles directly; they
// choose a model per sense, and each sense sets the roles it owns.
type Role struct {
	ID        string
	Reasoning bool
}

var Roles = []Role{
	{"agent", true},
	{"vision", true},
	{"routing", true},
	{"realtime", false},
	{"library", true},
	{"library-fallback", true},
	{"embedding", false},
	{"transcription", false},
	{"transcription-fallback", false},
	{"media-transcription", false},
	{"media-transcription-fallback", false},
	{"speech", false},
}

// Sense is a user-facing model choice. Kind says which Gateway models fit it.
type Sense struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Kind        string   `json:"-"`
	Roles       []string `json:"-"`
}

// Senses stay few on purpose. Routing, fallbacks, embeddings and speech keep
// Misty's defaults.
var Senses = []Sense{
	{"thinking", "Thinking", "Conversations, planning and using tools.", "thinking", []string{"agent"}},
	{"seeing", "Seeing", "Reading screenshots, using the browser and describing your files.", "seeing", []string{"vision", "library"}},
	{"listening", "Listening", "Turning voice notes and audio files into text.", "listening", []string{"transcription", "media-transcription"}},
	{"speaking", "Speaking", "The companion's live voice.", "speaking", []string{"realtime"}},
}

func FindSense(id string) (Sense, bool) {
	for _, s := range Senses {
		if s.ID == id {
			return s, true
		}
	}
	return Sense{}, false
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

func FindRole(id string) (Role, bool) {
	for _, r := range Roles {
		if r.ID == id {
			return r, true
		}
	}
	return Role{}, false
}
func ValidModel(model string) bool {
	return len(model) > 0 && len(model) <= 200 && !strings.ContainsAny(model, " \t\r\n\x00?#") && strings.Contains(model, "/") && !strings.HasPrefix(model, "/") && !strings.HasSuffix(model, "/")
}
func ValidReasoning(value string) bool {
	switch value {
	case "", "none", "low", "medium", "high", "xhigh", "max":
		return true
	}
	return false
}

// DirectOpenAI reports whether a model skips the Gateway: OpenAI models use
// the instance OpenAI key when one is set. The agent runtime applies the same
// rule; keep them in step.
func DirectOpenAI(model string) bool {
	return strings.HasPrefix(model, "openai/") && envconfig.OpenAIKey() != ""
}

// UsageProvider names who bills a model call.
func UsageProvider(model string) string {
	if DirectOpenAI(model) {
		return "openai"
	}
	return "ai-gateway"
}
