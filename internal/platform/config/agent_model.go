package config

import (
	"errors"
	"strings"
)

// AgentModelConfig is the instance's optional pinned default model.
type AgentModelConfig struct {
	Model string
}

func AgentModel() (AgentModelConfig, error) {
	c := AgentModelConfig{Model: strings.TrimSpace(Getenv("MISTY_AGENT_MODEL"))}
	if c.Model != "" {
		prefix, model, ok := strings.Cut(c.Model, "/")
		if !ok || prefix == "" || model == "" || len(c.Model) > 200 || strings.ContainsAny(c.Model, " \t\r\n") {
			return c, errors.New("MISTY_AGENT_MODEL must be a provider/model ID")
		}
	}
	return c, nil
}

// OpenAIKey is the instance's own OpenAI key. When set, OpenAI models call
// OpenAI directly instead of going through the AI Gateway.
func OpenAIKey() string { return strings.TrimSpace(Getenv("OPENAI_API_KEY")) }
