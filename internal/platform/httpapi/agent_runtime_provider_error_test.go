package api

import "testing"

func TestPublicAgentRuntimeProviderFailure(t *testing.T) {
	for _, tc := range []struct {
		code, want string
	}{
		{"model_provider_credit_exhausted", "Misty's AI provider has no available credit. The server administrator needs to add credit or configure another provider."},
		{"model_gateway_unavailable", "Misty's model providers are temporarily unavailable. Please try again shortly."},
		{"unknown_provider_failure", "Misty could not complete this request."},
	} {
		t.Run(tc.code, func(t *testing.T) {
			// Only the known public code may affect the receipt. Provider response
			// bodies and credentials must never reach the conversation.
			if got := publicAgentRuntimeFailure(tc.code, "private provider response with credentials"); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
