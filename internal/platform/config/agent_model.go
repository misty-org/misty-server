package config

import (
	"errors"
	"net/url"
	"strings"
)

// AgentModelConfig contains only public routing metadata, never credentials.
type AgentModelConfig struct {
	Provider string
	Model    string
}

func AgentModel() (AgentModelConfig, error) {
	c := AgentModelConfig{Provider: strings.TrimSpace(Getenv("MISTY_AGENT_MODEL_PROVIDER")), Model: strings.TrimSpace(Getenv("MISTY_AGENT_MODEL"))}
	if c.Provider == "" {
		c.Provider = "gateway"
	}
	key, base := strings.TrimSpace(Getenv("MISTY_AGENT_MODEL_API_KEY")), strings.TrimSpace(Getenv("MISTY_AGENT_MODEL_BASE_URL"))
	switch c.Provider {
	case "gateway", "openai", "anthropic", "google", "openai-compatible":
	default:
		return c, errors.New("invalid MISTY_AGENT_MODEL_PROVIDER")
	}
	if c.Model != "" {
		prefix, model, ok := strings.Cut(c.Model, "/")
		if !ok || prefix == "" || model == "" || len(c.Model) > 200 || strings.ContainsAny(c.Model, " \t\r\n") {
			return c, errors.New("MISTY_AGENT_MODEL must be a provider/model ID")
		}
	}
	if c.Provider != "gateway" {
		if !strings.HasPrefix(c.Model, c.Provider+"/") {
			return c, errors.New("MISTY_AGENT_MODEL must use the configured provider prefix")
		}
		if key == "" && c.Provider != "openai-compatible" {
			return c, errors.New("MISTY_AGENT_MODEL_API_KEY is required for the configured provider")
		}
		if c.Provider == "openai-compatible" && base == "" {
			return c, errors.New("MISTY_AGENT_MODEL_BASE_URL is required for openai-compatible")
		}
	} else if key != "" || base != "" {
		return c, errors.New("gateway credentials use AI_GATEWAY_API_KEY and AI_GATEWAY_BASE_URL")
	}
	if base != "" {
		u, err := url.Parse(base)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return c, errors.New("MISTY_AGENT_MODEL_BASE_URL must be an HTTP(S) URL without credentials, query, or fragment")
		}
	}
	return c, nil
}
