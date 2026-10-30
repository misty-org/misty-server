package api

import (
	"net/http"
	"strings"
	"time"
)

func (l *APIRateLimiter) limiterFor(method, path string, policy RateLimitPolicy) *SlidingWindowLimiter {
	key := method + " " + path

	l.Mu.Lock()
	defer l.Mu.Unlock()

	limiter, ok := l.Limiters[key]
	if ok {
		return limiter
	}
	if len(l.Limiters) >= MaxTrackedRoutes {
		key = method + " " + overflowRouteKey
		if overflow, exists := l.Limiters[key]; exists {
			return overflow
		}
	}
	limiter = NewSlidingWindowLimiter(policy.Limit, policy.Window)
	l.Limiters[key] = limiter
	return limiter
}

const overflowRouteKey = "{overflow}"

// costBearingRoutes spend money or third-party quota on the server's credentials, so
// they are charged per account rather than per address. Keyed by the normalized
// path the limiter already computes.
var costBearingRoutes = map[string]bool{
	"/ai/complete": true,
	// Egress and storage operations bill per byte and per request.
	"/spaces/{spaceID}/library/items/{id}/download":                true,
	"/spaces/{spaceID}/attachments/{id}/download":                  true,
	"/spaces/{spaceID}/notes/{id}/assets/uploads":                  true,
	"/spaces/{spaceID}/drawings/{id}/assets/uploads":               true,
	"/spaces/{spaceID}/notes/{id}/assets/uploads/{id}/finalize":    true,
	"/spaces/{spaceID}/drawings/{id}/assets/uploads/{id}/finalize": true,
}

func NormalizeRateLimitPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "/"
	}
	for _, prefix := range []string{"/api", "/v1"} {
		if trimmed == prefix {
			return "/"
		}
		if strings.HasPrefix(trimmed, prefix+"/") {
			trimmed = trimmed[len(prefix):]
			break
		}
	}
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	// Space-scoped routes carry ids in the path. Collapsing them keeps one
	// budget per route shape instead of handing out a fresh budget per Space —
	// and keeps the number of tracked route templates bounded.
	if parts[0] == "spaces" && len(parts) >= 2 {
		parts[1] = "{spaceID}"
		for index := 2; index < len(parts); index++ {
			if looksLikeRatePathIdentifier(parts[index]) {
				parts[index] = "{id}"
			}
		}
		return "/" + strings.Join(parts, "/")
	}
	return trimmed
}

// looksLikeRatePathIdentifier reports whether a segment is a generated id
// rather than a fixed route word. Misty ids are prefixed UUIDs ("msg_<uuid>")
// or bare UUID/hex, and provider ids are similar, so length plus a separator or
// pure hex is a reliable signal without a per-route table.
func looksLikeRatePathIdentifier(segment string) bool {
	// Long fixed route words containing a hyphen would otherwise look like
	// Misty's prefixed identifiers and silently miss their explicit policy.
	if fixedRatePathSegments[segment] {
		return false
	}
	if len(segment) < 16 {
		return false
	}
	if strings.ContainsAny(segment, "_-") {
		return true
	}
	for _, character := range segment {
		isHex := (character >= '0' && character <= '9') ||
			(character >= 'a' && character <= 'f') ||
			(character >= 'A' && character <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}

var fixedRatePathSegments = map[string]bool{
	"collaboration-ticket": true,
}

func retryAfterSeconds(duration time.Duration) int {
	if duration <= 0 {
		return 1
	}
	seconds := int(duration / time.Second)
	if duration%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return seconds
}

func ForgotPasswordRateLimitKey(r *http.Request, email string) string {
	return ClientIPFromRequest(r) + "|" + strings.ToLower(strings.TrimSpace(email))
}
