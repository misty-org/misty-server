package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

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
			request := httptest.NewRequest("POST", "https://internal.example/api/connections/google/authorize", nil)
			if got, want := TestingConnectedAccountCallbackURL(request, "google"), base+"/oauth/connections/google/callback"; got != want {
				t.Fatalf("connectedAccountCallbackURL() = %q, want %q", got, want)
			}
		})
	}
}

func TestProviderURLsKeepOriginOnlyConfigurationCompatible(t *testing.T) {
	t.Setenv("MISTY_PUBLIC_API_URL", "https://mistysys.com")
	request := httptest.NewRequest("POST", "https://internal.example/api/connections/google/authorize", nil)
	if got, want := TestingConnectedAccountCallbackURL(request, "google"), "https://mistysys.com/api/oauth/connections/google/callback"; got != want {
		t.Fatalf("connectedAccountCallbackURL() = %q, want %q", got, want)
	}
}

// A development tunnel terminates TLS and forwards the original hostname, so
// an unconfigured server must follow the tunnel rather than assume its own
// listening address. This is what lets a rotating tunnel URL work without
// touching server configuration.
func TestProviderCallbackFollowsForwardedHostWhenUnconfigured(t *testing.T) {
	t.Setenv("MISTY_PUBLIC_API_URL", "")
	request := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/connections/google/authorize", nil)
	request.Host = "house-gotten-extended-richmond.trycloudflare.com"
	request.Header.Set("X-Forwarded-Host", "house-gotten-extended-richmond.trycloudflare.com")
	request.Header.Set("X-Forwarded-Proto", "https")

	want := "https://house-gotten-extended-richmond.trycloudflare.com/api/oauth/connections/google/callback"
	if got := TestingConnectedAccountCallbackURL(request, "google"); got != want {
		t.Fatalf("connectedAccountCallbackURL() = %q, want %q", got, want)
	}
}

// Explicit configuration stays authoritative, so a forged forwarding header
// cannot move a production redirect target.
func TestConfiguredBaseOutranksForwardedHost(t *testing.T) {
	t.Setenv("MISTY_PUBLIC_API_URL", "https://mistysys.com/api")
	request := httptest.NewRequest("POST", "https://mistysys.com/api/connections/google/authorize", nil)
	request.Header.Set("X-Forwarded-Host", "attacker.example")

	want := "https://mistysys.com/api/oauth/connections/google/callback"
	if got := TestingConnectedAccountCallbackURL(request, "google"); got != want {
		t.Fatalf("connectedAccountCallbackURL() = %q, want %q", got, want)
	}
}

// A plain local run with no proxy in front still gets an http:// callback.
func TestProviderCallbackStaysHTTPForPlainLocalhost(t *testing.T) {
	t.Setenv("MISTY_PUBLIC_API_URL", "")
	request := httptest.NewRequest("POST", "http://localhost:8080/api/connections/google/authorize", nil)

	want := "http://localhost:8080/api/oauth/connections/google/callback"
	if got := TestingConnectedAccountCallbackURL(request, "google"); got != want {
		t.Fatalf("connectedAccountCallbackURL() = %q, want %q", got, want)
	}
}

func TestProviderCallbackFallbackPreservesRequestAPIPrefix(t *testing.T) {
	t.Setenv("MISTY_PUBLIC_API_URL", "")
	for _, item := range []struct{ path, want string }{
		{"/api/connections/google/authorize", "https://mistysys.com/api/oauth/connections/google/callback"},
		{"/api/v2/connections/google/authorize", "https://mistysys.com/api/v2/oauth/connections/google/callback"},
		{"/connections/google/authorize", "https://mistysys.com/oauth/connections/google/callback"},
	} {
		request := httptest.NewRequest("POST", "https://mistysys.com"+item.path, nil)
		if got := TestingConnectedAccountCallbackURL(request, "google"); got != item.want {
			t.Fatalf("connectedAccountCallbackURL(%q) = %q, want %q", item.path, got, item.want)
		}
	}
}
