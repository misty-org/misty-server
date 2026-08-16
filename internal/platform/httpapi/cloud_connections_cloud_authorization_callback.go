package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func (s *SpacesService) CloudAuthorizationCallback() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provider, code, state := chi.URLParam(r, "provider"), r.URL.Query().Get("code"), r.URL.Query().Get("state")
		if refusal := r.URL.Query().Get("error"); refusal != "" {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthDeniedByProvider, errors.New(TestingProviderRefusalDetail(r)))
			return
		}
		definition, exists := TestingCloudOAuthCatalog[provider]
		if !exists || code == "" || state == "" {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthMalformedRedirect, fmt.Errorf(
				"known_provider=%t code_present=%t state_present=%t", exists, code != "", state != "",
			))
			return
		}
		stored, err := s.database.ConsumeCloudOAuthState(r.Context(), hashProviderValue(state))
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
		plaintext, err := s.decryptProviderSecret(provider, stored.SecretCiphertext, stored.SecretNonce)
		var secret cloudOAuthSecret
		if err != nil || json.Unmarshal(plaintext, &secret) != nil {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthStaleState, err)
			return
		}
		token, err := exchangeCloudCode(r.Context(), definition, secret, code, TestingCloudCallbackURL(r, provider))
		if err != nil {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthExchangeFailed, err)
			return
		}
		if token.AccessToken == "" {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthTokenUnusable, nil)
			return
		}
		accountID, accountName := fetchCloudIdentity(r.Context(), provider, token)
		if accountID == "" {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthTokenUnusable, errors.New("provider identity lookup returned no account"))
			return
		}
		secret.Verifier, secret.Token = "", token
		encoded, _ := json.Marshal(secret)
		ciphertext, nonce, err := s.encryptProviderSecret(provider, encoded)
		if err != nil {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthNotSaved, err)
			return
		}
		var expiresAt *time.Time
		if token.ExpiresIn > 0 {
			value := time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
			expiresAt = &value
		}
		entitlements, err := s.database.EntitlementsForUser(r.Context(), stored.UserID)
		if err != nil {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthNotSaved, err)
			return
		}
		maximum := 1
		if entitlements.Plan != db.TierBasic {
			maximum = 0
		}
		item, err := s.database.SaveCloudConnection(r.Context(), db.CloudConnection{
			UserID: stored.UserID, Provider: provider, Name: stored.ConnectionName,
			AccountID: accountID, AccountDisplay: accountName,
			CredentialCiphertext: ciphertext, CredentialNonce: nonce, KeyVersion: s.keyVer,
			UsesCustomOAuthClient: secret.Custom, ExpiresAt: expiresAt,
		}, maximum)
		if errors.Is(err, db.ErrCloudConnectionLimit) {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthCloudConnectionLimit, err)
			return
		}
		if err != nil {
			TestingWriteOAuthCallbackFailure(w, provider, TestingOAuthNotSaved, err)
			return
		}
		// Compatibility bridge: when the legacy cloud OAuth client is the same
		// account-level client, adopt the token into the reusable vault and bind
		// this remote without asking the user to authorize Google twice.
		_ = s.adoptCloudConnectionIntoConnectedAccount(r.Context(), stored.UserID, item, secret, token, expiresAt)
		TestingWriteProviderCompletionPage(w, definition.Name, item.AccountDisplay)
	}
}

func (s *SpacesService) adoptCloudConnectionIntoConnectedAccount(ctx context.Context, userID string, cloud *db.CloudConnection, secret cloudOAuthSecret, token providerTokenEnvelope, expiresAt *time.Time) error {
	if cloud == nil || secret.Custom {
		return nil
	}
	accountProvider := ""
	switch cloud.Provider {
	case "drive":
		accountProvider = "google"
	case "onedrive":
		accountProvider = "microsoft"
	case "dropbox":
		accountProvider = "dropbox"
	}
	definition, exists := TestingConnectedAccountOAuthCatalog[accountProvider]
	if !exists || secret.ClientID != connectedAccountClientID(definition) || secret.ClientSecret != connectedAccountClientSecret(definition) {
		return nil
	}
	capabilities := []string{"files"}
	scopes := definition.CapabilityScopes["files"]
	if previous, err := s.database.ConnectedAccountByIdentity(ctx, userID, accountProvider, cloud.AccountID); err == nil {
		capabilities = mergeConnectedAccountValues(previous.Capabilities, capabilities)
		scopes = mergeConnectedAccountValues(previous.GrantedScopes, scopes)
	}
	if token.Scope != "" {
		scopes = mergeConnectedAccountValues(scopes, strings.Fields(strings.ReplaceAll(token.Scope, ",", " ")))
	}
	encoded, _ := json.Marshal(token)
	ciphertext, nonce, err := s.encryptConnectedAccountSecret(accountProvider, encoded)
	if err != nil {
		return err
	}
	account, err := s.database.SaveConnectedAccount(ctx, db.ConnectedAccount{
		UserID: userID, Provider: accountProvider, AccountID: cloud.AccountID,
		AccountDisplay: cloud.AccountDisplay, CredentialCiphertext: ciphertext,
		CredentialNonce: nonce, KeyVersion: s.keyVer, Capabilities: capabilities,
		GrantedScopes: scopes, ExpiresAt: expiresAt,
	})
	if err != nil {
		return err
	}
	_, err = s.database.BindConnectedAccountCloudConnection(ctx, userID, *account, cloud.Provider, cloud.Name, 0)
	return err
}

func (s *SpacesService) CloudConnectionToken() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		// Provider access tokens are never returned to authenticated renderer
		// sessions. Current desktop builds use the one-time native handoff flow.
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusGone, map[string]string{
			"code": "cloud_token_route_deprecated",
		})
	}
}

func (s *SpacesService) DeleteCloudConnection() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		connectionID := chi.URLParam(r, "connectionID")
		item, err := s.database.CloudConnection(r.Context(), userID, connectionID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		status := "binding_removed_account_preserved"
		var revokeErr error
		if item.ConnectedAccountID == "" {
			status, revokeErr = s.revokeCloudConnection(r.Context(), *item)
		}
		if err := s.database.DeleteCloudConnection(r.Context(), userID, connectionID); err != nil {
			writeSpaceError(w, err)
			return
		}
		if revokeErr != nil {
			status = "revocation_failed_local_credentials_erased"
		}
		w.Header().Set("X-Misty-Provider-Revocation", status)
		w.WriteHeader(http.StatusNoContent)
	}
}

func exchangeCloudCode(ctx context.Context, definition cloudOAuthDefinition, secret cloudOAuthSecret, code, redirect string) (providerTokenEnvelope, error) {
	values := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
		"client_id": {secret.ClientID}, "client_secret": {secret.ClientSecret}, "code_verifier": {secret.Verifier}}
	return requestCloudToken(ctx, definition.TokenURL, values)
}

func refreshCloudToken(ctx context.Context, definition cloudOAuthDefinition, secret cloudOAuthSecret) (providerTokenEnvelope, error) {
	if secret.Token.RefreshToken == "" {
		return providerTokenEnvelope{}, errors.New("refresh token is missing")
	}
	values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {secret.Token.RefreshToken},
		"client_id": {secret.ClientID}, "client_secret": {secret.ClientSecret}}
	token, err := requestCloudToken(ctx, definition.TokenURL, values)
	if token.RefreshToken == "" {
		token.RefreshToken = secret.Token.RefreshToken
	}
	return token, err
}

func requestCloudToken(ctx context.Context, endpoint string, values url.Values) (providerTokenEnvelope, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return providerTokenEnvelope{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := connectedAccountHTTPClient(20 * time.Second).Do(request)
	if err != nil {
		return providerTokenEnvelope{}, err
	}
	defer response.Body.Close()
	raw, err := readConnectedAccountResponse(response.Body)
	if err != nil {
		return providerTokenEnvelope{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providerTokenEnvelope{}, fmt.Errorf("cloud token endpoint returned %s", response.Status)
	}
	var token providerTokenEnvelope
	if err := json.Unmarshal(raw, &token); err != nil || token.AccessToken == "" {
		return providerTokenEnvelope{}, errors.New("cloud token response is invalid")
	}
	return token, nil
}

func fetchCloudIdentity(ctx context.Context, provider string, token providerTokenEnvelope) (string, string) {
	endpoint, method := "", http.MethodGet
	switch provider {
	case "drive":
		endpoint = "https://openidconnect.googleapis.com/v1/userinfo"
	case "dropbox":
		endpoint, method = "https://api.dropboxapi.com/2/users/get_current_account", http.MethodPost
	case "onedrive":
		endpoint = "https://graph.microsoft.com/v1.0/me?$select=id,displayName,userPrincipalName"
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return "", ""
	}
	request.Header.Set("Authorization", firstNonempty(token.TokenType, "Bearer")+" "+token.AccessToken)
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := connectedAccountHTTPClient(15 * time.Second).Do(request)
	if err != nil {
		return "", ""
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", ""
	}
	raw, err := readConnectedAccountResponse(response.Body)
	if err != nil {
		return "", ""
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return "", ""
	}
	id := firstProviderString(value, "account_id", "id", "sub")
	name := firstProviderString(value, "name", "display_name", "displayName", "email", "userPrincipalName")
	return id, name
}

func TestingCloudCallbackURL(r *http.Request, provider string) string {
	return requestPublicAPIBase(r) + "/oauth/cloud/" + url.PathEscape(provider) + "/callback"
}

func TestingCloudAPIBase(provider string) string {
	switch provider {
	case "drive":
		return "https://www.googleapis.com"
	case "dropbox":
		return "https://api.dropboxapi.com"
	case "onedrive":
		return "https://graph.microsoft.com/v1.0"
	default:
		return ""
	}
}
