package unit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
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
	definition := api.TestingConnectedAccountOAuthCatalog["figma"]
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

func TestFigmaConnectedAccountBasicAuthIdentityAndBounds(t *testing.T) {
	t.Setenv("FIGMA_CLIENT_ID", "figma-client")
	t.Setenv("FIGMA_CLIENT_SECRET", "figma-secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "figma-client" || password != "figma-secret" {
			t.Fatalf("basic auth=%q %q %t", username, password, ok)
		}
		_ = r.ParseForm()
		if r.Form.Get("client_id") != "" || r.Form.Get("client_secret") != "" {
			t.Fatalf("credentials in form: %v", r.Form)
		}
		_, _ = w.Write([]byte(`{"access_token":"figma-token","token_type":"Bearer"}`))
	}))
	defer server.Close()
	definition := api.TestingConnectedAccountOAuthCatalog["figma"]
	definition.TokenURL = server.URL
	if err := api.TestingRequestConnectedAccountToken(context.Background(), definition, url.Values{"client_id": {"wrong"}, "client_secret": {"wrong"}}); err != nil {
		t.Fatal(err)
	}
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"figma-user","handle":"Misty Designer"}`))
	}))
	defer identity.Close()
	definition.IdentityURL = identity.URL
	id, display := api.TestingFetchConnectedAccountIdentity(context.Background(), definition, "token", "Bearer")
	if id != "figma-user" || display != "Misty Designer" {
		t.Fatalf("identity=%q %q", id, display)
	}
	oversized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", api.TestingConnectedAccountResponseLimit+1)))
	}))
	defer oversized.Close()
	definition.TokenURL = oversized.URL
	if err := api.TestingRequestConnectedAccountToken(context.Background(), definition, url.Values{}); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized error=%v", err)
	}
}

func TestCloudConnectionMetadataAndLegacyBrokerKeepCredentialsServerSide(t *testing.T) {
	raw, err := json.Marshal(api.TestingCloudConnectionJSON(db.CloudConnection{ID: "cloud-1", Provider: "drive", CredentialCiphertext: []byte("encrypted-access-token"), CredentialNonce: []byte("secret-nonce")}))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"credential", "encrypted-access-token", "secret-nonce"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("metadata exposed %q: %s", forbidden, raw)
		}
	}
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("c", 32)))
	service, err := api.NewSpacesService(nil, nil, key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := service.TestingEncryptLegacyCloudAccessToken("drive", "legacy-provider-token")
	if err != nil {
		t.Fatal(err)
	}
	token, tokenType, err := service.TestingCloudConnectionAccessToken(context.Background(), "user-1", &db.CloudConnection{ID: "legacy", UserID: "user-1", Provider: "drive", CredentialCiphertext: ciphertext, CredentialNonce: nonce})
	if err != nil || token != "legacy-provider-token" || tokenType != "Bearer" {
		t.Fatalf("token=%q type=%q err=%v", token, tokenType, err)
	}
}
