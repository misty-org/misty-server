// Package composio is Misty's client for Composio Cloud sessions. Each Misty
// account maps to one pseudonymous Composio user whose session can reach every
// toolkit. Misty owns connection prompts, approvals and the effect journal;
// this package only speaks Composio's REST API.
package composio

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/kannachi323/misty/server/internal/platform/config"
)

var ErrUnavailable = errors.New("Composio is not configured")

const CloudEndpoint = "https://backend.composio.dev"

type Config struct{ APIKey string }

// ConfigFromEnv reads the project key. MISTY_COMPOSIO_API_KEY takes precedence
// over the dashboard's standard COMPOSIO_API_KEY. Every server has one
// (server/.env/composio.env).
func ConfigFromEnv() Config {
	key := strings.TrimSpace(config.Getenv("MISTY_COMPOSIO_API_KEY"))
	if key == "" {
		key = strings.TrimSpace(config.Getenv("COMPOSIO_API_KEY"))
	}
	return Config{APIKey: key}
}

// Validate requires a well-formed key.
func (c Config) Validate() error {
	if c.APIKey == "" || strings.ContainsAny(c.APIKey, " \t\r\n") {
		return ErrUnavailable
	}
	return nil
}

// UserID is the pseudonymous Composio user for one Misty account. Composio
// never receives Misty's user IDs or email addresses.
func UserID(owner string) string {
	h := sha256.Sum256([]byte("misty:composio:user:v1:" + owner))
	return "misty_" + hex.EncodeToString(h[:])
}
