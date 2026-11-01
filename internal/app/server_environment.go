package app

import (
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/kannachi323/misty/server/internal/platform/config"
)

// WebsiteURLFromEnv is the base the desktop-to-browser handoff redirects
// into. Must be https unless it targets localhost.
func WebsiteURLFromEnv() (string, error) {
	return validatedURLFromEnv("MISTY_WEBSITE_URL", "http://localhost:5174")
}

// AuthHandoffStartURLFromEnv is the API-origin URL the desktop app opens.
// The handoff writes the host-only HttpOnly session cookie on that API origin,
// then redirects to the website. The browser sends it on credentialed API
// requests even when the website is hosted on a separate allowed origin.
func AuthHandoffStartURLFromEnv() (string, error) {
	return validatedURLFromEnv("AUTH_HANDOFF_START_URL", "http://localhost:8080/auth/handoff/start")
}

func validatedURLFromEnv(name, fallback string) (string, error) {
	rawURL := config.Getenv(name)
	if rawURL == "" {
		rawURL = fallback
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid %s: %w", name, err)
	}
	if parsedURL.Host == "" {
		return "", fmt.Errorf("%s must include a host", name)
	}
	if parsedURL.Scheme == "https" {
		return parsedURL.String(), nil
	}
	if parsedURL.Scheme == "http" && IsLocalhostHostname(parsedURL.Hostname()) {
		return parsedURL.String(), nil
	}

	return "", fmt.Errorf("%s must use https unless it targets localhost", name)
}

func IsLocalhostHostname(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func warnOnProxyConfiguration() {
	if trustProxyHeadersEnabled() && strings.TrimSpace(config.Getenv("TRUSTED_PROXY_CIDRS")) == "" {
		log.Println("NOTICE: TRUST_PROXY_HEADERS is on and TRUSTED_PROXY_CIDRS is unset; " +
			"forwarded headers are honoured only from loopback and private ranges")
	}
}

func trustProxyHeadersEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(config.Getenv("TRUST_PROXY_HEADERS"))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}
