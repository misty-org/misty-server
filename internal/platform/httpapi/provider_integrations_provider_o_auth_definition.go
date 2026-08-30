package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
	"github.com/lib/pq"
)

type providerOAuthDefinition struct {
	ID, Name, AuthorizeURL, TokenURL, ClientIDEnv, ClientSecretEnv string
	Scopes                                                         []string
	PKCE                                                           bool
}

var TestingProviderOAuthCatalog = map[string]providerOAuthDefinition{}

type providerOAuthAvailability struct {
	Provider   string `json:"provider"`
	Configured bool   `json:"configured"`
}

func TestingProviderOAuthAvailabilityCatalog() []providerOAuthAvailability {
	providers := make([]providerOAuthAvailability, 0, len(TestingProviderOAuthCatalog)+1)
	for provider, definition := range TestingProviderOAuthCatalog {
		providers = append(providers, providerOAuthAvailability{
			Provider:   provider,
			Configured: TestingProviderOAuthClientID(definition) != "" && TestingProviderOAuthClientSecret(definition) != "",
		})
	}
	providers = append(providers, providerOAuthAvailability{Provider: "github", Configured: strings.TrimSpace(envconfig.Getenv("GITHUB_APP_ID")) != "" && strings.TrimSpace(envconfig.Getenv("GITHUB_APP_SLUG")) != "" && strings.TrimSpace(envconfig.Getenv("GITHUB_APP_PRIVATE_KEY")) != "" && strings.TrimSpace(envconfig.Getenv("GITHUB_WEBHOOK_SECRET")) != ""})
	sort.Slice(providers, func(i, j int) bool { return providers[i].Provider < providers[j].Provider })
	return providers
}

func TestingProviderOAuthClientID(definition providerOAuthDefinition) string {
	return strings.TrimSpace(envconfig.Getenv(definition.ClientIDEnv))
}

func TestingProviderOAuthClientSecret(definition providerOAuthDefinition) string {
	return strings.TrimSpace(envconfig.Getenv(definition.ClientSecretEnv))
}

type providerTokenEnvelope struct {
	AccessToken   string                     `json:"access_token"`
	RefreshToken  string                     `json:"refresh_token,omitempty"`
	TokenType     string                     `json:"token_type,omitempty"`
	Scope         string                     `json:"scope,omitempty"`
	ExpiresIn     int                        `json:"expires_in,omitempty"`
	IDToken       string                     `json:"id_token,omitempty"`
	Team          *struct{ ID, Name string } `json:"team,omitempty"`
	WorkspaceID   string                     `json:"workspace_id,omitempty"`
	WorkspaceName string                     `json:"workspace_name,omitempty"`
}

func (s *SpacesService) BeginProviderAuthorization() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		provider := chi.URLParam(r, "provider")
		definition, ok := TestingProviderOAuthCatalog[provider]
		if !ok {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		clientID := TestingProviderOAuthClientID(definition)
		if clientID == "" || TestingProviderOAuthClientSecret(definition) == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "provider_not_configured", "provider": provider})
			return
		}
		var body struct {
			ReturnTo string `json:"return_to"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if !TestingValidProviderReturnPath(body.ReturnTo) {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		state := randomProviderValue(32)
		verifier := randomProviderValue(48)
		ciphertext, nonce, err := s.encryptProviderSecret(provider, []byte(verifier))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		expires := time.Now().UTC().Add(10 * time.Minute)
		if err := s.database.CreateProviderOAuthState(r.Context(), hashProviderValue(state), db.ProviderOAuthState{UserID: userID, SpaceID: chi.URLParam(r, "spaceID"), Provider: provider, VerifierCiphertext: ciphertext, VerifierNonce: nonce, ReturnTo: body.ReturnTo, ExpiresAt: expires}); err != nil {
			writeSpaceError(w, err)
			return
		}
		callback := TestingProviderCallbackURL(r, provider)
		params := url.Values{"client_id": {clientID}, "redirect_uri": {callback}, "response_type": {"code"}, "state": {state}}
		if len(definition.Scopes) > 0 {
			params.Set("scope", strings.Join(definition.Scopes, " "))
		}
		if definition.PKCE {
			sum := sha256.Sum256([]byte(verifier))
			params.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
			params.Set("code_challenge_method", "S256")
		}
		switch provider {
		case "google":
			params.Set("access_type", "offline")
			params.Set("prompt", "consent")
			params.Set("include_granted_scopes", "true")
		case "discord":
			params.Set("permissions", "274878024704")
		case "notion":
			params.Set("owner", "user")
		}
		writeJSON(w, http.StatusOK, map[string]any{"provider": provider, "authorization_url": definition.AuthorizeURL + "?" + params.Encode(), "state_expires_at": expires})
	}
}

func TestingValidProviderReturnPath(value string) bool {
	if value == "" {
		return true
	}
	return strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.ContainsAny(value, "\\\r\n")
}

func (s *SpacesService) ProviderAuthorizationCallback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provider, code, state := chi.URLParam(r, "provider"), r.URL.Query().Get("code"), r.URL.Query().Get("state")
		// Providers report a refused or cancelled consent as `error` rather than
		// by omitting the code, and that is an ordinary outcome rather than a
		// fault, so it is named separately from a malformed redirect.
		if refusal := r.URL.Query().Get("error"); refusal != "" {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthDeniedByProvider, errors.New(TestingProviderRefusalDetail(r)))
			return
		}
		definition, exists := TestingProviderOAuthCatalog[provider]
		if !exists || code == "" || state == "" {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthMalformedRedirect, fmt.Errorf(
				"known_provider=%t code_present=%t state_present=%t", exists, code != "", state != "",
			))
			return
		}
		stored, err := s.database.ConsumeProviderOAuthState(r.Context(), hashProviderValue(state))
		if err != nil {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthStaleState, err)
			return
		}
		if stored.Provider != provider {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthStaleState, fmt.Errorf(
				"state belongs to provider %q", stored.Provider,
			))
			return
		}
		verifier, err := s.decryptProviderSecret(provider, stored.VerifierCiphertext, stored.VerifierNonce)
		if err != nil {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthStaleState, err)
			return
		}
		token, raw, err := exchangeProviderCode(r.Context(), definition, code, string(verifier), TestingProviderCallbackURL(r, provider))
		if err != nil {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthExchangeFailed, err)
			return
		}
		if token.AccessToken == "" {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthTokenUnusable, nil)
			return
		}
		accountID, accountName := providerAccountIdentity(provider, token, raw)
		if accountID == "" {
			accountID, accountName = fetchProviderAccountIdentity(r.Context(), provider, token)
		}
		if accountID == "" {
			accountID = hashProviderValue(token.AccessToken)[:16]
		}
		if accountName == "" {
			accountName = definition.Name + " account"
		}
		ciphertext, nonce, err := s.encryptProviderSecret(provider, raw)
		if err != nil {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthNotSaved, err)
			return
		}
		var expiresAt *time.Time
		if token.ExpiresIn > 0 {
			value := time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
			expiresAt = &value
		}
		scopes := definition.Scopes
		if token.Scope != "" {
			scopes = strings.Fields(strings.ReplaceAll(token.Scope, ",", " "))
		}
		_, err = s.database.SaveProviderCredential(r.Context(), db.ProviderCredential{SpaceID: stored.SpaceID, UserID: stored.UserID, Provider: provider, Ciphertext: ciphertext, Nonce: nonce, KeyVersion: s.keyVer, AccountID: accountID, AccountDisplay: accountName, ExpiresAt: expiresAt}, accountName, scopes)
		if err != nil {
			logProviderCallbackDatabaseFailure(provider, err)
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthNotSaved, err)
			return
		}
		// Setup intent is optional. A provider connected later from Settings may
		// have no setup row, so absence is deliberately ignored.
		_ = s.database.SetSpaceSetupProviderStatus(
			r.Context(), stored.UserID, stored.SpaceID, provider, "authorized",
		)
		TestingWriteProviderCompletionPage(w, definition.Name, accountName)
	}
}

func logProviderCallbackDatabaseFailure(provider string, err error) {
	var databaseError *pq.Error
	if errors.As(err, &databaseError) {
		log.Printf("Provider OAuth callback persistence failed: provider=%s sqlstate=%s table=%s constraint=%s", provider, databaseError.Code, databaseError.Table, databaseError.Constraint)
		return
	}
	log.Printf("Provider OAuth callback persistence failed: provider=%s error_type=%T", provider, err)
}

func (s *SpacesService) DeleteProviderIntegration() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		if err := s.database.DeleteProviderIntegration(r.Context(), userID, chi.URLParam(r, "integrationID")); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *SpacesService) encryptProviderSecret(provider string, plaintext []byte) ([]byte, []byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return s.aead.Seal(nil, nonce, plaintext, []byte("misty-provider-v2:"+provider)), nonce, nil
}

func (s *SpacesService) decryptProviderSecret(provider string, ciphertext, nonce []byte) ([]byte, error) {
	plaintext, err := s.aead.Open(nil, nonce, ciphertext, []byte("misty-provider-v2:"+provider))
	if err == nil || provider != "google" {
		return plaintext, err
	}
	// Existing Google Calendar credentials were sealed with the legacy provider
	// identifier. The migration changes only metadata, so retain a read path until
	// each credential is refreshed and sealed under the shared Google identity.
	return s.aead.Open(nil, nonce, ciphertext, []byte("misty-provider-v2:google_calendar"))
}

func (s *SpacesService) TestingEncryptProviderAccessToken(provider, accessToken string) ([]byte, []byte, error) {
	raw, err := json.Marshal(providerTokenEnvelope{AccessToken: accessToken, TokenType: "Bearer"})
	if err != nil {
		return nil, nil, err
	}
	return s.encryptProviderSecret(provider, raw)
}
