package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestCloudOAuthCatalogUsesDirectProviderEndpoints(t *testing.T) {
	expected := map[string]string{
		"drive":    "accounts.google.com",
		"dropbox":  "dropbox.com",
		"onedrive": "microsoftonline.com",
	}
	for provider, host := range expected {
		definition, ok := TestingCloudOAuthCatalog[provider]
		if !ok {
			t.Fatalf("%s is missing", provider)
		}
		if !strings.Contains(definition.AuthorizeURL, host) || definition.TokenURL == "" {
			t.Fatalf("%s OAuth endpoints are incomplete", provider)
		}
		if definition.ClientIDEnv == "" || definition.ClientSecretEnv == "" {
			t.Fatalf("%s Misty OAuth environment variables are incomplete", provider)
		}
	}
}

func TestCloudCallbackURLKeepsAPIPrefix(t *testing.T) {
	t.Setenv("MISTY_PUBLIC_API_URL", "")
	request := httptest.NewRequest("POST", "http://localhost:8080/api/cloud/connections/drive/authorize", nil)
	got := TestingCloudCallbackURL(request, "drive")
	want := "http://localhost:8080/api/oauth/cloud/drive/callback"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCloudAPIBaseMatchesProvider(t *testing.T) {
	for provider, expected := range map[string]string{
		"drive": "googleapis.com", "dropbox": "dropboxapi.com", "onedrive": "microsoft.com",
	} {
		if got := TestingCloudAPIBase(provider); !strings.Contains(got, expected) {
			t.Fatalf("%s API base %q does not contain %q", provider, got, expected)
		}
	}
}
