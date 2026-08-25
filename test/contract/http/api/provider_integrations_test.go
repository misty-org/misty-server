package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestProviderOAuthAvailabilityCatalogReportsServerConfiguration(t *testing.T) {
	for _, definition := range TestingProviderOAuthCatalog {
		t.Setenv(definition.ClientIDEnv, "")
		t.Setenv(definition.ClientSecretEnv, "")
	}
	t.Setenv("GITHUB_APP_ID", "gh-app")
	t.Setenv("GITHUB_APP_SLUG", "gh-slug")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", "gh-key")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "gh-secret")

	providers := TestingProviderOAuthAvailabilityCatalog()
	if len(providers) != 1 {
		t.Fatalf("provider availability count = %d, want 1", len(providers))
	}
	for index, provider := range providers {
		if index > 0 && providers[index-1].Provider > provider.Provider {
			t.Fatalf("provider availability is not sorted: %+v", providers)
		}
		if provider.Provider != "github" {
			t.Fatalf("unexpected provider %q was advertised", provider.Provider)
		}
		if !provider.Configured {
			t.Fatalf("github provider should be configured")
		}
	}
}

func TestProviderOAuthCatalogEmpty(t *testing.T) {
	for _, forbidden := range []string{"google", "slack", "discord", "notion", "apple_calendar", "gmail", "outlook_mail", "outlook_calendar", "microsoft_teams", "google_drive", "onedrive", "sharepoint", "dropbox", "jira", "zoom", "webhook", "custom_webhook", "obsidian"} {
		if _, exists := TestingProviderOAuthCatalog[forbidden]; exists {
			t.Fatalf("forbidden provider %q is registered", forbidden)
		}
	}
}

func TestProviderReturnPathRejectsExternalAndHeaderInjection(t *testing.T) {
	for _, valid := range []string{"", "/spaces/space-1/agents", "/oauth/complete?tab=connections"} {
		if !TestingValidProviderReturnPath(valid) {
			t.Fatalf("expected %q to be valid", valid)
		}
	}
	for _, invalid := range []string{"https://example.com", "//example.com", `/\\example.com`, "/safe\r\nLocation: https://example.com"} {
		if TestingValidProviderReturnPath(invalid) {
			t.Fatalf("expected %q to be rejected", invalid)
		}
	}
}

func TestProviderCompletionPageTellsUserToReturnToMisty(t *testing.T) {
	recorder := httptest.NewRecorder()
	TestingWriteProviderCompletionPage(recorder, "GitHub", "alex@example.com")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/html; charset=utf-8", contentType)
	}
	body := recorder.Body.String()
	for _, want := range []string{"GitHub is connected", "alex@example.com", "Return to the Misty app", "You can close this browser tab"} {
		if !strings.Contains(body, want) {
			t.Fatalf("completion page missing %q in %s", want, body)
		}
	}
	if strings.Contains(body, "misty://") {
		t.Fatalf("completion page should not emit a custom protocol link: %s", body)
	}
}

func TestProviderURLsUseConfiguredFullAPIBaseWithoutDuplicatingPath(t *testing.T) {
	for _, base := range []string{"https://mistysys.com/api", "https://mistysys.com/api/v2"} {
		t.Run(base, func(t *testing.T) {
			t.Setenv("MISTY_PUBLIC_API_URL", base)
			request := httptest.NewRequest("POST", "https://internal.example/api/spaces/space-1/integrations/github/authorize", nil)
			if got, want := TestingProviderCallbackURL(request, "github"), base+"/oauth/providers/github/callback"; got != want {
				t.Fatalf("providerCallbackURL() = %q, want %q", got, want)
			}
		})
	}
}

func TestProviderURLsKeepOriginOnlyConfigurationCompatible(t *testing.T) {
	t.Setenv("MISTY_PUBLIC_API_URL", "https://mistysys.com")
	request := httptest.NewRequest("POST", "https://internal.example/api/spaces/space-1/integrations/github/authorize", nil)
	if got, want := TestingProviderCallbackURL(request, "github"), "https://mistysys.com/api/oauth/providers/github/callback"; got != want {
		t.Fatalf("providerCallbackURL() = %q, want %q", got, want)
	}
}

// A development tunnel terminates TLS and forwards the original hostname, so
// an unconfigured server must follow the tunnel rather than assume its own
// listening address. This is what lets a rotating tunnel URL work without
// touching server configuration.
func TestProviderCallbackFollowsForwardedHostWhenUnconfigured(t *testing.T) {
	t.Setenv("MISTY_PUBLIC_API_URL", "")
	request := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/spaces/space-1/integrations/github/authorize", nil)
	request.Host = "house-gotten-extended-richmond.trycloudflare.com"
	request.Header.Set("X-Forwarded-Host", "house-gotten-extended-richmond.trycloudflare.com")
	request.Header.Set("X-Forwarded-Proto", "https")

	want := "https://house-gotten-extended-richmond.trycloudflare.com/api/oauth/providers/github/callback"
	if got := TestingProviderCallbackURL(request, "github"); got != want {
		t.Fatalf("providerCallbackURL() = %q, want %q", got, want)
	}
}

// Explicit configuration stays authoritative, so a forged forwarding header
// cannot move a production redirect target.
func TestConfiguredBaseOutranksForwardedHost(t *testing.T) {
	t.Setenv("MISTY_PUBLIC_API_URL", "https://mistysys.com/api")
	request := httptest.NewRequest("POST", "https://mistysys.com/api/spaces/space-1/integrations/github/authorize", nil)
	request.Header.Set("X-Forwarded-Host", "attacker.example")

	want := "https://mistysys.com/api/oauth/providers/github/callback"
	if got := TestingProviderCallbackURL(request, "github"); got != want {
		t.Fatalf("providerCallbackURL() = %q, want %q", got, want)
	}
}

// A plain local run with no proxy in front still gets an http:// callback.
func TestProviderCallbackStaysHTTPForPlainLocalhost(t *testing.T) {
	t.Setenv("MISTY_PUBLIC_API_URL", "")
	request := httptest.NewRequest("POST", "http://localhost:8080/api/spaces/space-1/integrations/github/authorize", nil)

	want := "http://localhost:8080/api/oauth/providers/github/callback"
	if got := TestingProviderCallbackURL(request, "github"); got != want {
		t.Fatalf("providerCallbackURL() = %q, want %q", got, want)
	}
}

func TestProviderCallbackFallbackPreservesRequestAPIPrefix(t *testing.T) {
	t.Setenv("MISTY_PUBLIC_API_URL", "")
	for _, item := range []struct{ path, want string }{
		{"/api/spaces/space-1/integrations/github/authorize", "https://mistysys.com/api/oauth/providers/github/callback"},
		{"/api/v2/spaces/space-1/integrations/github/authorize", "https://mistysys.com/api/v2/oauth/providers/github/callback"},
		{"/spaces/space-1/integrations/github/authorize", "https://mistysys.com/oauth/providers/github/callback"},
	} {
		request := httptest.NewRequest("POST", "https://mistysys.com"+item.path, nil)
		if got := TestingProviderCallbackURL(request, "github"); got != item.want {
			t.Fatalf("providerCallbackURL(%q) = %q, want %q", item.path, got, item.want)
		}
	}
}
