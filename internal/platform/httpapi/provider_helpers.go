package api

import (
	"net/url"
	"strings"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

func firstProviderString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := value[key].(string); ok && text != "" {
			return text
		}
	}
	return ""
}

func configuredPublicAPIBase() string {
	base := strings.TrimRight(strings.TrimSpace(envconfig.Getenv("MISTY_PUBLIC_API_URL")), "/")
	if base == "" {
		return ""
	}
	parsed, err := url.Parse(base)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" && (parsed.Path == "" || parsed.Path == "/") {
		// Compatibility for existing origin-only deployments. New configuration
		// should always include the complete API path explicitly.
		return base + "/api"
	}
	return base
}
