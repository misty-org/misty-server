package api

import (
	"strings"
	"time"
)

var signInRoutePolicies = map[string]RateLimitPolicy{
	"POST /auth/device":         {Limit: 10, Window: time.Minute},
	"GET /auth/device/{id}":     {Limit: 60, Window: time.Minute},
	"GET /auth/device/lookup":   {Limit: 10, Window: time.Minute},
	"POST /auth/device/approve": {Limit: 10, Window: time.Minute},
	"POST /auth/device/refuse":  {Limit: 10, Window: time.Minute},
	"POST /auth/integration":    {Limit: 60, Window: time.Minute},
	"DELETE /me/sessions/{id}":  {Limit: 30, Window: time.Minute},
}

var fixedDeviceSignInSegments = map[string]bool{"lookup": true, "approve": true, "refuse": true}

func addSignInRoutePolicies(policies map[string]RateLimitPolicy) {
	for route, policy := range signInRoutePolicies {
		policies[route] = policy
	}
}

func normalizeSignInRateLimitPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 3 || parts[2] == "" {
		return path
	}
	switch {
	case parts[0] == "auth" && parts[1] == "device" && !fixedDeviceSignInSegments[parts[2]]:
		return "/auth/device/{id}"
	case parts[0] == "me" && parts[1] == "sessions":
		return "/me/sessions/{id}"
	}
	return path
}
