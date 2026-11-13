package api

import (
	"errors"
	"net/url"
	"strings"
)

// ValidatePublicURL accepts an absolute https URL, or http on localhost for
// development: the rule for every address the server sends people to.
func ValidatePublicURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if parsed.Host == "" {
		return errors.New("URL must include a host")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme == "http" && isLocalhostHost(parsed.Hostname()) {
		return nil
	}
	return errors.New("URL must use https unless it targets localhost")
}

func isLocalhostHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}
