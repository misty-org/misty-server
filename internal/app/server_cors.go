package app

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/kannachi323/misty/server/internal/platform/config"
)

func AllowedCORSOrigins() []string {
	origins := []string{
		"tauri://localhost",
		"http://tauri.localhost",
		"https://tauri.localhost",
	}
	// Web origins (a hosted web app, for one) come only from the operator.
	// Local development servers are credentialed origins only outside
	// production; anything on the user's machine could otherwise serve them.
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

// allowedCORSRequestHeaders is every header the desktop client may send.
//
// A header missing here fails at the preflight with a 200 that carries no
// Access-Control-Allow-Origin, which surfaces in the browser as an opaque
// "load failed" rather than anything pointing at CORS.
var allowedCORSRequestHeaders = []string{
	"Accept",
	"Authorization",
	"Content-Type",
	"Idempotency-Key",
	"X-Request-ID",
	"X-Misty-Platform",
	"X-Misty-CSRF",
	"X-Misty-Release-Channel",
	"X-Misty-Session-Id",
	"X-Misty-Analytics-Enabled",
	"X-Misty-Device-Timestamp",
	"X-Misty-Device-Nonce",
	"X-Misty-Device-Signature",
	"X-Misty-Attachment-Upload-Token",
	"X-Misty-Library-Upload-Token",
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
