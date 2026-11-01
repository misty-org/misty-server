package app

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/kannachi323/misty/server/internal/platform/config"
)

// productionRequired is every value a production server needs. Nothing is
// optional: the integrations (Cloudflare, Composio, Google) are parts of every
// server, and the setup container generates the secrets. The server sends no
// email, and holds no search key: web search runs on each device (docs/byok.md).
var productionRequired = []string{
	"DB_HOST", "DB_USER", "DB_PASSWORD", "DB_NAME", "MISTY_PUBLIC_API_URL",
	"SPACE_LINK_ENCRYPTION_KEY", "MISTY_AUTH_SIGNING_KEY", "MISTY_AGENT_RUNTIME_CONTROL_SECRET",
	"MISTY_DEVICE_TICKET_PRIVATE_KEY", "MISTY_DEVICE_PAIRING_PEPPER",
	"R2_ENDPOINT", "R2_BUCKET", "R2_ACCESS_KEY", "R2_SECRET_KEY",
	"PARTYKIT_HOST", "JOURNAL_COLLAB_TICKET_PRIVATE_KEY", "JOURNAL_COLLAB_CONTROL_SECRET",
	"JOURNAL_COLLAB_PROJECTION_SECRET", "JOURNAL_COLLAB_ROOM_SALT",
	"MISTY_WEB_FETCH_URL", "MISTY_WEB_FETCH_SECRET",
	"MISTY_COMPOSIO_API_KEY", "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET",
}

// productionForbidden are model keys and retired operator model settings. A
// server never calls a model: agents and every model call run on the person's
// desktop, on that device's own keys (docs/byok.md). A server that still sets
// one stops at startup, so a stale configuration is noticed.
var productionForbidden = []string{
	"AI_GATEWAY_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "OPENROUTER_API_KEY",
	"NVIDIA_API_KEY", "ELEVENLABS_API_KEY", "SONIOX_API_KEY", "OLLAMA_API_KEY", "OPENAI_COMPATIBLE_API_KEY",
	"VERCEL_OIDC_TOKEN", "MISTY_AGENT_MODEL", "MISTY_AGENT_RUNTIME_URL",
}

// ValidateProductionEnvironment rejects a production configuration that is
// missing any part of the server. Feature constructors validate each value's
// format after this check.
func ValidateProductionEnvironment() error {
	if !productionEnvironment() {
		return nil
	}
	for _, name := range productionRequired {
		if strings.TrimSpace(config.Getenv(name)) == "" {
			return fmt.Errorf("%s is required in production", name)
		}
	}
	for _, name := range productionForbidden {
		if strings.TrimSpace(config.Getenv(name)) != "" {
			return fmt.Errorf("%s is set, but a Misty server holds no model keys or models; remove it (models run on each device)", name)
		}
	}
	publicURL, err := url.Parse(strings.TrimSpace(config.Getenv("MISTY_PUBLIC_API_URL")))
	if err != nil || publicURL.Scheme != "https" || publicURL.Host == "" {
		return fmt.Errorf("MISTY_PUBLIC_API_URL must be an absolute https URL in production")
	}
	if rawPort := strings.TrimSpace(config.Getenv("PORT")); rawPort != "" {
		port, err := strconv.Atoi(rawPort)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("PORT must be an integer between 1 and 65535")
		}
	}
	return nil
}
