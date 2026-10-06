package unit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestConnectedAccountTransportBlocksCredentialRedirects(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.FormValue("client_secret") != "" {
			leaked.Store(true)
		}
		_, _ = w.Write([]byte(`{"id":"attacker"}`))
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	definition := api.TestingConnectedAccountOAuthCatalog["google"]
	definition.TokenURL, definition.IdentityURL = redirect.URL, redirect.URL
	if err := api.TestingRequestConnectedAccountToken(context.Background(), definition, url.Values{}); err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatalf("redirect token error=%v", err)
	}
	if id, _ := api.TestingFetchConnectedAccountIdentity(context.Background(), definition, "secret-token", "Bearer"); id != "" {
		t.Fatalf("identity crossed redirect: %q", id)
	}
	if leaked.Load() {
		t.Fatal("provider credentials crossed redirect")
	}
}

func TestConnectedAccountTokenResponseIsBounded(t *testing.T) {
	oversized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", api.TestingConnectedAccountResponseLimit+1)))
	}))
	defer oversized.Close()
	definition := api.TestingConnectedAccountOAuthCatalog["google"]
	definition.TokenURL = oversized.URL
	if err := api.TestingRequestConnectedAccountToken(context.Background(), definition, url.Values{}); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized error=%v", err)
	}
}
