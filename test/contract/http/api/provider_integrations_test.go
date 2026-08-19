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
	t.Setenv("GOOGLE_CLIENT_ID", "google-client")
	t.Setenv("GOOGLE_CLIENT_SECRET", "google-secret")
	t.Setenv("GITHUB_APP_ID", "")
	t.Setenv("GITHUB_APP_SLUG", "")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", "")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "")

	providers := TestingProviderOAuthAvailabilityCatalog()
	if len(providers) != 5 {
		t.Fatalf("provider availability count = %d, want 5", len(providers))
	}
	for index, provider := range providers {
		if index > 0 && providers[index-1].Provider > provider.Provider {
			t.Fatalf("provider availability is not sorted: %+v", providers)
		}
		if provider.Configured != (provider.Provider == "google") {
			t.Fatalf("provider %q configured = %v", provider.Provider, provider.Configured)
		}
		if provider.Provider != "google" && provider.Provider != "slack" && provider.Provider != "discord" && provider.Provider != "notion" && provider.Provider != "github" {
			t.Fatalf("unexpected provider %q was advertised", provider.Provider)
		}
	}
}

func TestGoogleProviderOAuthUsesOnlyCanonicalServerEnvironmentNames(t *testing.T) {
	t.Setenv("GOOGLE_OAUTH_CLIENT_ID", "ignored-oauth-client")
	t.Setenv("GOOGLE_OAUTH_CLIENT_SECRET", "ignored-oauth-secret")
	t.Setenv("GOOGLE_CLIENT_ID", "canonical-client")
	t.Setenv("GOOGLE_CLIENT_SECRET", "canonical-secret")

	definition := TestingProviderOAuthCatalog["google"]
	if definition.ClientIDEnv != "GOOGLE_CLIENT_ID" || definition.ClientSecretEnv != "GOOGLE_CLIENT_SECRET" {
		t.Fatalf("Google environment contract = %q/%q", definition.ClientIDEnv, definition.ClientSecretEnv)
	}
	if got := TestingProviderOAuthClientID(definition); got != "canonical-client" {
		t.Fatalf("Google client ID = %q", got)
	}
	if got := TestingProviderOAuthClientSecret(definition); got != "canonical-secret" {
		t.Fatalf("Google client secret = %q", got)
	}
	t.Setenv("GOOGLE_CLIENT_ID", "")
	t.Setenv("GOOGLE_CLIENT_SECRET", "")
	if got := TestingProviderOAuthClientID(definition); got != "" {
		t.Fatalf("deprecated OAuth client ID was accepted: %q", got)
	}
	if got := TestingProviderOAuthClientSecret(definition); got != "" {
		t.Fatalf("deprecated OAuth client secret was accepted: %q", got)
	}
	availability := TestingProviderOAuthAvailabilityCatalog()
	for _, provider := range availability {
		if provider.Provider == "google" && provider.Configured {
			t.Fatal("Google should be unavailable when only deprecated OAuth environment names are set")
		}
	}
}

func TestLegacyGoogleCalendarConnectionCanReadAndWriteEvents(t *testing.T) {
	joined := strings.Join(TestingProviderOAuthCatalog["google"].Scopes, " ")
	for _, required := range []string{"calendar.readonly", "calendar.events"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("legacy Google Calendar consent missing %q: %s", required, joined)
		}
	}
	if strings.Contains(joined, "gmail") {
		t.Fatalf("Calendar consent unexpectedly requested Gmail: %s", joined)
	}
}

func TestProviderOAuthCatalogMatchesLaunchContract(t *testing.T) {
	want := []string{"google", "slack", "discord", "notion"}
	for _, provider := range want {
		definition, ok := TestingProviderOAuthCatalog[provider]
		if !ok || definition.AuthorizeURL == "" || definition.TokenURL == "" || definition.ClientIDEnv == "" || definition.ClientSecretEnv == "" {
			t.Fatalf("provider %q is not production-configurable: %+v", provider, definition)
		}
	}
	if len(TestingProviderOAuthCatalog) != len(want) {
		t.Fatalf("unexpected providers in catalog: got %d want %d", len(TestingProviderOAuthCatalog), len(want))
	}
	for _, forbidden := range []string{"apple_calendar", "gmail", "outlook_mail", "outlook_calendar", "microsoft_teams", "google_drive", "onedrive", "sharepoint", "dropbox", "github", "jira", "zoom", "webhook", "custom_webhook", "obsidian"} {
		if _, exists := TestingProviderOAuthCatalog[forbidden]; exists {
			t.Fatalf("forbidden provider %q is registered", forbidden)
		}
	}
}

func TestGoogleCalendarPreconditionFailureMapsToConflict(t *testing.T) {
	if got := TestingProviderErrorCodeForStatus(http.StatusPreconditionFailed); got != "conflict" {
		t.Fatalf("Google precondition failure code = %q, want conflict", got)
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
	TestingWriteProviderCompletionPage(recorder, "Google", "alex@example.com")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/html; charset=utf-8", contentType)
	}
	body := recorder.Body.String()
	for _, want := range []string{"Google is connected", "alex@example.com", "Return to the Misty app", "You can close this browser tab"} {
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
			request := httptest.NewRequest("POST", "https://internal.example/api/spaces/space-1/integrations/google/authorize", nil)
			if got, want := TestingProviderCallbackURL(request, "google"), base+"/oauth/providers/google/callback"; got != want {
				t.Fatalf("providerCallbackURL() = %q, want %q", got, want)
			}
			if got, want := TestingProviderInfrastructureURL("google", "calendar"), base+"/provider-callbacks/google/calendar"; got != want {
				t.Fatalf("providerInfrastructureURL() = %q, want %q", got, want)
			}
		})
	}
}

func TestProviderURLsKeepOriginOnlyConfigurationCompatible(t *testing.T) {
	t.Setenv("MISTY_PUBLIC_API_URL", "https://mistysys.com")
	request := httptest.NewRequest("POST", "https://internal.example/api/spaces/space-1/integrations/notion/authorize", nil)
	if got, want := TestingProviderCallbackURL(request, "notion"), "https://mistysys.com/api/oauth/providers/notion/callback"; got != want {
		t.Fatalf("providerCallbackURL() = %q, want %q", got, want)
	}
}

// A development tunnel terminates TLS and forwards the original hostname, so
// an unconfigured server must follow the tunnel rather than assume its own
// listening address. This is what lets a rotating tunnel URL work without
// touching server configuration.
func TestProviderCallbackFollowsForwardedHostWhenUnconfigured(t *testing.T) {
	t.Setenv("MISTY_PUBLIC_API_URL", "")
	request := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/spaces/space-1/integrations/notion/authorize", nil)
	request.Host = "house-gotten-extended-richmond.trycloudflare.com"
	request.Header.Set("X-Forwarded-Host", "house-gotten-extended-richmond.trycloudflare.com")
	request.Header.Set("X-Forwarded-Proto", "https")

	want := "https://house-gotten-extended-richmond.trycloudflare.com/api/oauth/providers/notion/callback"
	if got := TestingProviderCallbackURL(request, "notion"); got != want {
		t.Fatalf("providerCallbackURL() = %q, want %q", got, want)
	}
}

// Explicit configuration stays authoritative, so a forged forwarding header
// cannot move a production redirect target.
func TestConfiguredBaseOutranksForwardedHost(t *testing.T) {
	t.Setenv("MISTY_PUBLIC_API_URL", "https://mistysys.com/api")
	request := httptest.NewRequest("POST", "https://mistysys.com/api/spaces/space-1/integrations/notion/authorize", nil)
	request.Header.Set("X-Forwarded-Host", "attacker.example")

	want := "https://mistysys.com/api/oauth/providers/notion/callback"
	if got := TestingProviderCallbackURL(request, "notion"); got != want {
		t.Fatalf("providerCallbackURL() = %q, want %q", got, want)
	}
}

// A plain local run with no proxy in front still gets an http:// callback.
func TestProviderCallbackStaysHTTPForPlainLocalhost(t *testing.T) {
	t.Setenv("MISTY_PUBLIC_API_URL", "")
	request := httptest.NewRequest("POST", "http://localhost:8080/api/spaces/space-1/integrations/notion/authorize", nil)

	want := "http://localhost:8080/api/oauth/providers/notion/callback"
	if got := TestingProviderCallbackURL(request, "notion"); got != want {
		t.Fatalf("providerCallbackURL() = %q, want %q", got, want)
	}
}

func TestProviderCallbackFallbackPreservesRequestAPIPrefix(t *testing.T) {
	t.Setenv("MISTY_PUBLIC_API_URL", "")
	for _, item := range []struct{ path, want string }{
		{"/api/spaces/space-1/integrations/slack/authorize", "https://mistysys.com/api/oauth/providers/slack/callback"},
		{"/api/v2/spaces/space-1/integrations/slack/authorize", "https://mistysys.com/api/v2/oauth/providers/slack/callback"},
		{"/spaces/space-1/integrations/slack/authorize", "https://mistysys.com/oauth/providers/slack/callback"},
	} {
		request := httptest.NewRequest("POST", "https://mistysys.com"+item.path, nil)
		if got := TestingProviderCallbackURL(request, "slack"); got != item.want {
			t.Fatalf("providerCallbackURL(%q) = %q, want %q", item.path, got, item.want)
		}
	}
}
