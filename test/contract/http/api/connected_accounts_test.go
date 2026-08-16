package api

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func TestConnectedAccountOAuthCatalogHasMailCapabilities(t *testing.T) {
	for _, provider := range []string{"google", "microsoft"} {
		definition, ok := TestingConnectedAccountOAuthCatalog[provider]
		if !ok || definition.AuthorizeURL == "" || definition.TokenURL == "" || definition.IdentityURL == "" {
			t.Fatalf("%s connected-account OAuth definition is incomplete", provider)
		}
		capabilities, scopes, valid := TestingConnectedAccountRequestedScopes(definition, []string{"mail"})
		if !valid || len(capabilities) != 1 || capabilities[0] != "mail" || len(scopes) < 2 {
			t.Fatalf("%s mail consent = capabilities %#v scopes %#v valid %v", provider, capabilities, scopes, valid)
		}
	}
}

func TestConnectedAccountOAuthCatalogHasReusableFileCapabilities(t *testing.T) {
	for provider, expectedScope := range map[string]string{
		"google":    "https://www.googleapis.com/auth/drive",
		"microsoft": "Files.ReadWrite.All",
		"dropbox":   "files.content.write",
	} {
		definition, ok := TestingConnectedAccountOAuthCatalog[provider]
		if !ok {
			t.Fatalf("%s connected-account definition is missing", provider)
		}
		capabilities, scopes, valid := TestingConnectedAccountRequestedScopes(definition, []string{"files"})
		if !valid || len(capabilities) != 1 || capabilities[0] != "files" || !containsValue(scopes, expectedScope) {
			t.Fatalf("%s files consent = capabilities %#v scopes %#v valid %v", provider, capabilities, scopes, valid)
		}
	}
}

func TestConnectedAccountOAuthRejectsUnknownCapabilities(t *testing.T) {
	definition := TestingConnectedAccountOAuthCatalog["google"]
	if _, _, valid := TestingConnectedAccountRequestedScopes(definition, []string{"mail", "unknown"}); valid {
		t.Fatal("unknown connected-account capability was accepted")
	}
}

func TestConnectedAccountCallbackURLAndIncrementalConsent(t *testing.T) {
	request := httptest.NewRequest("POST", "http://localhost:8080/api/connections/google/authorize", nil)
	if got, want := TestingConnectedAccountCallbackURL(request, "google"), "http://localhost:8080/api/oauth/connections/google/callback"; got != want {
		t.Fatalf("callback URL = %q, want %q", got, want)
	}
	definition := TestingConnectedAccountOAuthCatalog["google"]
	_, scopes, _ := TestingConnectedAccountRequestedScopes(definition, []string{"mail", "mail"})
	joined := strings.Join(scopes, " ")
	if !strings.Contains(joined, "gmail.modify") || !strings.Contains(joined, "gmail.send") {
		t.Fatalf("Google mail scopes = %#v", scopes)
	}
	if strings.Contains(url.QueryEscape(joined), "calendar") {
		t.Fatalf("mail consent unexpectedly requested Calendar: %#v", scopes)
	}
}

func TestGoogleCalendarConsentIsIncrementalAndLeastPrivilege(t *testing.T) {
	definition := TestingConnectedAccountOAuthCatalog["google"]
	_, readScopes, valid := TestingConnectedAccountRequestedScopes(definition, []string{"calendar_read"})
	if !valid || !containsValue(readScopes, "https://www.googleapis.com/auth/calendar.readonly") {
		t.Fatalf("Google calendar read consent = %#v, valid %v", readScopes, valid)
	}
	if containsValue(readScopes, "https://www.googleapis.com/auth/calendar.events") {
		t.Fatalf("read-only Calendar consent requested write access: %#v", readScopes)
	}
	capabilities, writeScopes, valid := TestingConnectedAccountRequestedScopes(definition, []string{"mail", "calendar_write"})
	if !valid || !containsValue(capabilities, "mail") || !containsValue(capabilities, "calendar_write") {
		t.Fatalf("incremental capabilities = %#v, valid %v", capabilities, valid)
	}
	for _, scope := range []string{
		"https://www.googleapis.com/auth/calendar.readonly",
		"https://www.googleapis.com/auth/calendar.events",
		"https://www.googleapis.com/auth/gmail.modify",
	} {
		if !containsValue(writeScopes, scope) {
			t.Fatalf("incremental Google consent missing %q: %#v", scope, writeScopes)
		}
	}
}

func TestGoogleCalendarSpaceBindingRequiresExactGrantedCapability(t *testing.T) {
	account := db.ConnectedAccount{
		Provider: "google", Status: "active", Capabilities: []string{"calendar_read"},
	}
	if !TestingConnectedAccountCanBind(account, "google", "calendar_read") {
		t.Fatal("active Google Calendar read capability could not be bound")
	}
	if TestingConnectedAccountCanBind(account, "google", "calendar_write") {
		t.Fatal("read-only Google account was allowed to bind Calendar writes")
	}
	account.Capabilities = append(account.Capabilities, "calendar_write")
	if !TestingConnectedAccountCanBind(account, "google", "calendar_write") {
		t.Fatal("granted Google Calendar write capability could not be bound")
	}
	account.Status = "needs_attention"
	if TestingConnectedAccountCanBind(account, "google", "calendar_read") {
		t.Fatal("unhealthy connected account was allowed to bind")
	}
}

func containsValue(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestConnectedAccountResponseJSONContract(t *testing.T) {
	expires := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	raw, err := json.Marshal(ConnectedAccountResponse{
		ID: "connection-1", Provider: "google", AccountID: "account-1",
		AccountDisplay: "owner@example.com", Capabilities: []string{"mail"},
		GrantedScopes: []string{"gmail.modify", "gmail.send"}, Status: "active", ExpiresAt: &expires,
	})
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		t.Fatalf("invalid connected-account JSON: %s", raw)
	}
	for _, key := range []string{"id", "provider", "account_id", "account_display", "capabilities", "granted_scopes", "status", "expires_at"} {
		if _, exists := value[key]; !exists {
			t.Fatalf("connected-account JSON missing %q: %s", key, raw)
		}
	}
	for _, forbidden := range []string{"credential_ciphertext", "credential_nonce", "access_token", "refresh_token"} {
		if _, exists := value[forbidden]; exists {
			t.Fatalf("connected-account JSON exposed %q: %s", forbidden, raw)
		}
	}
}
