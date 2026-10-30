// Package byok carries the model keys and choices an account brings from its
// device. The desktop sends them on every request in X-Misty-Models; the
// server keeps the latest copy in memory only, never in the database or logs.
package byok

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

// Header is the request header the desktop sends the account's models in.
const Header = "X-Misty-Models"

// MaxHeaderBytes bounds the encoded header. A larger one is ignored.
const MaxHeaderBytes = 16 << 10

// ErrModelKeyNeeded means neither the account nor the operator has a model
// for an AI request.
var ErrModelKeyNeeded = errors.New("add a model key in Settings → Models")

// Provider IDs a model ID may start with.
var providers = map[string]bool{
	"openai": true, "anthropic": true, "google": true, "openrouter": true, "gateway": true,
	"nvidia": true, "ollama": true, "openai-compatible": true, "elevenlabs": true, "soniox": true,
}

// aliases are model ID prefixes that name another provider's key.
var aliases = map[string]string{"nim": "nvidia", "gemini": "google"}

// Credential is one provider's key, as the agent runtime receives it.
type Credential struct {
	Provider string `json:"provider"`
	APIKey   string `json:"apiKey"`
	BaseURL  string `json:"baseURL,omitempty"`
}

// Models are the account's choices per role. Only Agent is required.
type Models struct {
	Agent         string `json:"agent"`
	Fast          string `json:"fast,omitempty"`
	Embedding     string `json:"embedding,omitempty"`
	Transcription string `json:"transcription,omitempty"`
	Speech        string `json:"speech,omitempty"`
}

// Credentials is the decoded header.
type Credentials struct {
	Version   int                   `json:"version"`
	Providers map[string]Credential `json:"providers"`
	Models    Models                `json:"models"`
	Reasoning string                `json:"reasoning,omitempty"`
}

var reasoningLevels = map[string]bool{"none": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true}

// Decode reads the header value: base64url (padding optional) of UTF-8 JSON.
// It returns an error for anything malformed; callers ignore the header then.
func Decode(value string) (*Credentials, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > MaxHeaderBytes {
		return nil, errors.New("models header is empty or too large")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(value, "="))
	if err != nil {
		return nil, errors.New("models header is not base64url")
	}
	var c Credentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, errors.New("models header is not JSON")
	}
	if c.Version != 1 {
		return nil, errors.New("models header version is not 1")
	}
	clean := map[string]Credential{}
	for id, credential := range c.Providers {
		id = strings.TrimSpace(id)
		if !providers[id] {
			continue
		}
		credential.Provider = id
		credential.APIKey = strings.TrimSpace(credential.APIKey)
		credential.BaseURL = strings.TrimSpace(credential.BaseURL)
		if len(credential.APIKey) > 4096 || len(credential.BaseURL) > 2048 {
			return nil, errors.New("models header credential is too large")
		}
		if credential.BaseURL != "" && !strings.HasPrefix(credential.BaseURL, "https://") && !strings.HasPrefix(credential.BaseURL, "http://") {
			return nil, errors.New("models header baseURL is not http(s)")
		}
		if id == "openai-compatible" && credential.BaseURL == "" {
			continue
		}
		// Ollama runs without a key; every other provider needs one.
		if credential.APIKey == "" && id != "ollama" {
			continue
		}
		clean[id] = credential
	}
	c.Providers = clean
	for _, model := range []*string{&c.Models.Agent, &c.Models.Fast, &c.Models.Embedding, &c.Models.Transcription, &c.Models.Speech} {
		*model = strings.TrimSpace(*model)
		if *model != "" && !WellFormed(*model) {
			return nil, errors.New("models header names a malformed model")
		}
	}
	if c.Reasoning = strings.ToLower(strings.TrimSpace(c.Reasoning)); c.Reasoning != "" && !reasoningLevels[c.Reasoning] {
		c.Reasoning = ""
	}
	return &c, nil
}

// WellFormed reports whether id is "<provider>/<model>" for a known provider.
func WellFormed(id string) bool {
	if len(id) > 300 || strings.ContainsAny(id, " \t\r\n") {
		return false
	}
	_, model, ok := strings.Cut(id, "/")
	return ok && model != "" && ProviderOf(id) != ""
}

// ProviderOf is the provider whose key runs a model ID, or "" when the
// prefix is unknown.
func ProviderOf(model string) string {
	prefix, _, _ := strings.Cut(strings.TrimSpace(model), "/")
	if alias := aliases[prefix]; alias != "" {
		prefix = alias
	}
	if providers[prefix] {
		return prefix
	}
	return ""
}

// RoleModel is the account's model for an internal role: "agent" covers
// thinking, seeing and browsing; "fast" covers routing and summaries and
// falls back to the agent model; embeddings, transcription and speech have
// no fallback. "" means the account chose none.
func (c *Credentials) RoleModel(role string) string {
	if c == nil {
		return ""
	}
	switch role {
	case "agent", "vision", "browsing":
		return c.Models.Agent
	case "routing", "dictation-cleanup", "voice-notes", "day-summary", "fast":
		if c.Models.Fast != "" {
			return c.Models.Fast
		}
		return c.Models.Agent
	case "embedding":
		return c.Models.Embedding
	case "transcription", "transcription-fallback", "media-transcription", "media-transcription-fallback", "live-transcription", "diarized-transcription":
		return c.Models.Transcription
	case "speech":
		return c.Models.Speech
	}
	return ""
}

// CredentialFor is the key that runs a model, if the account has one.
func (c *Credentials) CredentialFor(model string) (Credential, bool) {
	if c == nil {
		return Credential{}, false
	}
	credential, ok := c.Providers[ProviderOf(model)]
	return credential, ok
}

// Usable reports whether the account can run anything at all: it chose an
// agent model and holds its provider's key.
func (c *Credentials) Usable() bool {
	if c == nil || c.Models.Agent == "" {
		return false
	}
	_, ok := c.CredentialFor(c.Models.Agent)
	return ok
}

type contextKey struct{}

// WithCredentials returns ctx carrying the request's credentials.
func WithCredentials(ctx context.Context, c *Credentials) context.Context {
	return context.WithValue(ctx, contextKey{}, c)
}

// FromContext is the credentials the request carried, if any.
func FromContext(ctx context.Context) *Credentials {
	c, _ := ctx.Value(contextKey{}).(*Credentials)
	return c
}
