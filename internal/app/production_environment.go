package app

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/misty-org/misty-server/internal/platform/config"
)

var productionRequired = []string{
	"DB_HOST", "DB_USER", "DB_PASSWORD", "DB_NAME", "MISTY_PUBLIC_API_URL",
	"SPACE_LINK_ENCRYPTION_KEY", "MISTY_AUTH_SIGNING_KEY",
	"R2_ENDPOINT", "R2_BUCKET", "R2_ACCESS_KEY", "R2_SECRET_KEY",
	"PARTYKIT_HOST", "JOURNAL_COLLAB_TICKET_PRIVATE_KEY", "JOURNAL_COLLAB_CONTROL_SECRET",
	"JOURNAL_COLLAB_PROJECTION_SECRET", "JOURNAL_COLLAB_ROOM_SALT",
	"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET",
}

var productionForbidden = []string{
	"AI_GATEWAY_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "OPENROUTER_API_KEY",
	"NVIDIA_API_KEY", "ELEVENLABS_API_KEY", "SONIOX_API_KEY", "OLLAMA_API_KEY", "OPENAI_COMPATIBLE_API_KEY",
	"VERCEL_OIDC_TOKEN", "MISTY_AGENT_MODEL", "MISTY_AGENT_RUNTIME_URL",
	"MISTY_AGENT_RUNTIME_CONTROL_SECRET", "MISTY_COMPOSIO_API_KEY",
}

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
			return fmt.Errorf("%s is set, but a Misty server holds no model keys or models; remove it (models run on each person's computer)", name)
		}
	}
	publicURL, err := url.Parse(strings.TrimSpace(config.Getenv("MISTY_PUBLIC_API_URL")))
	if err != nil || publicURL.Scheme != "https" || publicURL.Host == "" {
		return fmt.Errorf("MISTY_PUBLIC_API_URL must be an absolute https URL in production")
	}
	if !strings.HasSuffix(strings.TrimRight(publicURL.Path, "/"), "/v1") {
		return fmt.Errorf("MISTY_PUBLIC_API_URL must end in /v1, such as https://misty.example.com/v1: the server answers only under /v1")
	}
	if rawPort := strings.TrimSpace(config.Getenv("PORT")); rawPort != "" {
		port, err := strconv.Atoi(rawPort)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("PORT must be an integer between 1 and 65535")
		}
	}
	return nil
}
