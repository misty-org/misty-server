package app

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/misty-org/misty-server/internal/platform/config"
)

func AllowedCORSOrigins() []string {
	origins := []string{
		"tauri://localhost",
		"http://tauri.localhost",
		"https://tauri.localhost",
	}
	if !productionEnvironment() {
		origins = append(origins, "http://localhost:5173", "http://127.0.0.1:5173")
	}
	for _, origin := range strings.Split(config.Getenv("MISTY_ALLOWED_ORIGINS"), ",") {
		origin = strings.TrimSpace(origin)
		if origin != "" && !strings.Contains(origin, "*") {
			origins = append(origins, origin)
		}
	}
	return origins
}

var allowedCORSRequestHeaders = []string{
	"Accept",
	"Authorization",
	"Content-Type",
	"Idempotency-Key",
	"X-Request-ID",
	"X-Misty-Platform",
	"X-Misty-App-Version",
	"X-Misty-CSRF",
	"X-Misty-Release-Channel",
	"X-Misty-Library-Upload-Token",
	"If-Match",
	"X-Misty-Device",
	"X-Misty-Device-Name",
}

func productionEnvironment() bool {
	return strings.EqualFold(strings.TrimSpace(config.Getenv("MISTY_ENVIRONMENT")), "production")
}

func IsAllowedCORSOrigin(origin string) bool {
	for _, allowed := range AllowedCORSOrigins() {
		if strings.EqualFold(origin, allowed) {
			return true
		}
	}
	if productionEnvironment() {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "http" || !IsLocalhostHostname(parsed.Hostname()) || parsed.Path != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	port, err := strconv.Atoi(parsed.Port())
	return err == nil && port >= 5173 && port <= 5222
}
