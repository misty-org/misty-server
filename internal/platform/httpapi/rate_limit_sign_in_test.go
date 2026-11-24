package api

import "testing"

func TestSignInRoutesFoldTheirIDsIntoOneLimit(t *testing.T) {
	cases := map[string]string{
		"/v1/auth/device/dsi_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG": "/auth/device/{id}",
		"/v1/auth/device/lookup":                               "/auth/device/lookup",
		"/v1/auth/device/approve":                              "/auth/device/approve",
		"/v1/auth/device":                                      "/auth/device",
		"/v1/me/sessions/ses_0123456789abcdef0123456789abcdef": "/me/sessions/{id}",
		"/v1/me/sessions":                                      "/me/sessions",
		"/v1/spaces/space_abcdefabcdefabcdef/integrations/int_abcdefghijklmnopqrstuvwxyz/token": "/spaces/{spaceID}/integrations/{id}/token",
	}
	for input, want := range cases {
		if got := normalizeSignInRateLimitPath(NormalizeRateLimitPath(input)); got != want {
			t.Fatalf("%q normalized to %q, want %q", input, got, want)
		}
	}
	limiter := NewAPIRateLimiter()
	for route := range signInRoutePolicies {
		if _, ok := limiter.RoutePolicies[route]; !ok {
			t.Fatalf("%s has no rate limit", route)
		}
	}
}
