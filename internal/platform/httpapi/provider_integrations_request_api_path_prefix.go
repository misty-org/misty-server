package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
)

// requestPublicAPIBase is the origin+path an OAuth provider should redirect
// back to. MISTY_PUBLIC_API_URL is authoritative when set, because production
// must never derive a redirect target from a header a caller controls.
//
// Without it, the base is reconstructed from the request itself. That is what
// makes a development tunnel work with no configuration at all: the callback
// follows whatever hostname the request actually arrived on, so a rotating
// tunnel address needs no server-side change. Providers only honour redirect
// URIs that are already registered with them, which is what keeps the derived
// value from being a redirection primitive.
func requestPublicAPIBase(r *http.Request) string {
	if base := configuredPublicAPIBase(); base != "" {
		return base
	}
	host := r.Host
	if forwarded := forwardedHeaderValue(r, "X-Forwarded-Host"); forwarded != "" {
		host = forwarded
	}
	scheme := "https"
	switch {
	case forwardedHeaderValue(r, "X-Forwarded-Proto") != "":
		scheme = forwardedHeaderValue(r, "X-Forwarded-Proto")
	case r.TLS == nil && isLoopbackHost(host):
		scheme = "http"
	}
	return scheme + "://" + host + requestAPIPathPrefix(r.URL.Path)
}

// forwardedHeaderValue reads the first entry of a comma-separated forwarding
// header, which is the value the outermost proxy recorded.
func forwardedHeaderValue(r *http.Request, name string) string {
	value := r.Header.Get(name)
	if value == "" {
		return ""
	}
	if comma := strings.IndexByte(value, ','); comma >= 0 {
		value = value[:comma]
	}
	return strings.TrimSpace(value)
}

func isLoopbackHost(host string) bool {
	return strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1")
}

func requestAPIPathPrefix(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] != "api" {
		return ""
	}
	prefix := "/api"
	if len(parts) > 1 && isAPIVersionSegment(parts[1]) {
		prefix += "/" + parts[1]
	}
	return prefix
}

func isAPIVersionSegment(value string) bool {
	if len(value) < 2 || value[0] != 'v' {
		return false
	}
	for _, char := range value[1:] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func randomProviderValue(size int) string {
	value := make([]byte, size)
	_, _ = rand.Read(value)
	return base64.RawURLEncoding.EncodeToString(value)
}

func hashProviderValue(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
