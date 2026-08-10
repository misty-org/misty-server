package api

import (
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestNormalizeHandoffPath(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty_defaults_to_settings", raw: "", want: "/settings"},
		{name: "whitespace_defaults_to_settings", raw: "   ", want: "/settings"},
		{name: "allowlisted_root", raw: "/settings", want: "/settings"},
		{name: "allowlisted_tab", raw: "/settings/billing", want: "/settings/billing"},
		{name: "trailing_slash_trimmed", raw: "/settings/usage/", want: "/settings/usage"},
		{name: "bare_slash_defaults", raw: "/", want: "/settings"},

		// Everything below must be refused: this handler sits on a trusted API
		// origin, so an open redirect here is a phishing primitive.
		{name: "absolute_url", raw: "https://evil.example.com/settings", want: ""},
		{name: "scheme_relative", raw: "//evil.example.com", want: ""},
		{name: "traversal", raw: "/settings/../../admin", want: ""},
		{name: "unlisted_path", raw: "/admin", want: ""},
		{name: "unlisted_settings_child", raw: "/settings/danger", want: ""},
		{name: "relative", raw: "settings", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TestingNormalizeHandoffPath(tt.raw); got != tt.want {
				t.Fatalf("TestingNormalizeHandoffPath(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestNewAuthHandoffServiceRejectsInsecureURLs(t *testing.T) {
	tests := []struct {
		name       string
		startURL   string
		websiteURL string
		ok         bool
	}{
		{name: "https_pair", startURL: "https://app.example.com/auth/handoff/start", websiteURL: "https://app.example.com", ok: true},
		{name: "localhost_pair", startURL: "http://localhost:8080/auth/handoff/start", websiteURL: "http://localhost:5174", ok: true},
		{name: "plain_http_start", startURL: "http://example.com/auth/handoff/start", websiteURL: "https://app.example.com", ok: false},
		{name: "plain_http_website", startURL: "https://app.example.com/auth/handoff/start", websiteURL: "http://example.com", ok: false},
		{name: "hostless_website", startURL: "https://app.example.com/auth/handoff/start", websiteURL: "/settings", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A zero-value Database is non-nil, so construction gets past the nil
			// check and actually exercises the URL rules. No query is issued.
			_, err := NewAuthHandoffService(&db.Database{}, tt.startURL, tt.websiteURL)
			if (err == nil) != tt.ok {
				t.Fatalf("NewAuthHandoffService(%q, %q) error = %v, want ok=%v", tt.startURL, tt.websiteURL, err, tt.ok)
			}
		})
	}
}

func TestNewAuthHandoffServiceRequiresDatabase(t *testing.T) {
	if _, err := NewAuthHandoffService(nil, "https://app.example.com/auth/handoff/start", "https://app.example.com"); err == nil {
		t.Fatal("NewAuthHandoffService() with nil database should error")
	}
}
