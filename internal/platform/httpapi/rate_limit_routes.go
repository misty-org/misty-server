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
	_, explicit := l.RoutePolicies[key]
	if !explicit && len(l.Limiters) >= MaxTrackedRoutes {
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

var accountKeyedRoutes = map[string]bool{
	"/spaces/{spaceID}/notes/{id}/collaboration-ticket":            true,
	"/spaces/{spaceID}/code-sessions/{id}/collaboration-ticket":    true,
	"/spaces/{spaceID}/drive/items/{id}/collaboration-ticket":      true,
	"/spaces/{spaceID}/apps/{id}/rooms/{id}/collaboration-ticket": true,
}

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
			if namedRatePathIdentifier(parts, index) || looksLikeRatePathIdentifier(parts[index]) {
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

func namedRatePathIdentifier(parts []string, index int) bool {
	if fixedRatePathSegments[parts[index]] || parts[2] != "apps" {
		return false
	}
	switch index {
	case 3:
		return true
	case 5:
		return parts[4] == "rooms"
	}
	return false
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
