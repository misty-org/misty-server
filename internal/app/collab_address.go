package app

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/misty-org/misty-server/internal/platform/config"
)

func collabURLFromEnv() string {
	if explicit := strings.TrimRight(strings.TrimSpace(config.Getenv("MISTY_COLLAB_URL")), "/"); explicit != "" {
		return explicit
	}
	return publicAPIOrigin()
}

func publicAPIOrigin() string {
	parsed, err := url.Parse(strings.TrimSpace(config.Getenv("MISTY_PUBLIC_API_URL")))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func CollabURL(r *http.Request) string {
	if address := collabURLFromEnv(); address != "" {
		return address
	}
	if r == nil || r.Host == "" {
		return ""
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
