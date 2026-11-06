package api

import (
	"testing"

	. "github.com/misty-org/misty-server/internal/platform/httpapi"
)

func TestValidatePublicURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		ok   bool
	}{
		{name: "https", raw: "https://app.example.com/handoff", ok: true},
		{name: "localhost", raw: "http://localhost:5173/handoff", ok: true},
		{name: "missing_host", raw: "/handoff", ok: false},
		{name: "http_non_localhost", raw: "http://example.com/handoff", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePublicURL(tt.raw)
			if (err == nil) != tt.ok {
				t.Fatalf("ValidatePublicURL(%q) error = %v, want ok=%v", tt.raw, err, tt.ok)
			}
		})
	}
}
