package api

import (
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

var costBearingRoutes = map[string]bool{
	"/spaces/{spaceID}/attachments/{id}/download":               true,
	"/spaces/{spaceID}/notes/{id}/assets/uploads":               true,
	"/spaces/{spaceID}/notes/{id}/assets/uploads/{id}/finalize": true,
}

func NormalizeRateLimitPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "/"
	}
	if trimmed == "/v1" {
		return "/"
	}
	if strings.HasPrefix(trimmed, "/v1/") {
		trimmed = trimmed[len("/v1"):]
	}
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if parts[0] == "spaces" && len(parts) >= 2 {
		parts[1] = "{spaceID}"
		for index := 2; index < len(parts); index++ {
			if looksLikeRatePathIdentifier(parts[index]) {
				parts[index] = "{id}"
			}
		}
		return "/" + strings.Join(parts, "/")
	}
	if parts[0] == "invites" && len(parts) == 2 {
		return "/invites/{code}"
	}
	if parts[0] == "operator" && len(parts) >= 4 && parts[2] == "accounts" {
		parts[3] = "{id}"
		return "/" + strings.Join(parts, "/")
	}
	return trimmed
}

func looksLikeRatePathIdentifier(segment string) bool {
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
