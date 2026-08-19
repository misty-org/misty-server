package api

import (
	"encoding/json"
	"net/http"
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

func TestConnectedAccountsMapToExistingCloudEngineProviders(t *testing.T) {
	for accountProvider, cloudProvider := range map[string]string{
		"google": "drive", "microsoft": "onedrive", "dropbox": "dropbox",
	} {
		got, valid := TestingCloudProviderForConnectedAccount(accountProvider)
		if !valid || got != cloudProvider {
			t.Fatalf("connected account %q mapped to %q valid=%v, want %q", accountProvider, got, valid, cloudProvider)
		}
	}
	if _, valid := TestingCloudProviderForConnectedAccount("unknown"); valid {
		t.Fatal("unknown connected account mapped to a cloud provider")
	}
}

func TestLegacyCloudTokenRouteIsExplicitlyTokenFree(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/cloud/connections/cloud-1/token", nil)
	(&SpacesService{}).CloudConnectionToken().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusGone {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusGone)
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["code"] != "cloud_token_route_deprecated" {
		t.Fatalf("response = %#v", response)
	}
	for _, forbidden := range []string{"access_token", "refresh_token", "token_type"} {
		if _, exists := response[forbidden]; exists {
			t.Fatalf("deprecated token response exposed %q: %s", forbidden, recorder.Body.String())
		}
	}
}
