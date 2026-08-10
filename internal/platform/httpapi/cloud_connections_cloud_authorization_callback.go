package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
		definition, exists := TestingCloudOAuthCatalog[provider]
		if !exists || code == "" || state == "" {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		stored, err := s.database.ConsumeCloudOAuthState(r.Context(), hashProviderValue(state))
		if err != nil || stored.Provider != provider {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		plaintext, err := s.decryptProviderSecret(provider, stored.SecretCiphertext, stored.SecretNonce)
		var secret cloudOAuthSecret
		if err != nil || json.Unmarshal(plaintext, &secret) != nil {
			writeSpaceError(w, errors.New("cloud authorization state is invalid"))
			return
		}
		token, err := exchangeCloudCode(r.Context(), definition, secret, code, TestingCloudCallbackURL(r, provider))
		if err != nil || token.AccessToken == "" {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "cloud_exchange_failed"})
			return
		}
		accountID, accountName := fetchCloudIdentity(r.Context(), provider, token)
		if accountID == "" {
			writeJSON(w, http.StatusBadGateway, map[string]string{"code": "cloud_identity_failed"})
			return
		}
		secret.Verifier, secret.Token = "", token
		encoded, _ := json.Marshal(secret)
		ciphertext, nonce, err := s.encryptProviderSecret(provider, encoded)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		var expiresAt *time.Time
		if token.ExpiresIn > 0 {
			value := time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
			expiresAt = &value
		}
		entitlements, err := s.database.EntitlementsForUser(r.Context(), stored.UserID)
		if err != nil {
			writeSpaceError(w, errors.New("account license is unavailable"))
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
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "cloud_connection_limit", "message": "Basic accounts can connect one cloud account."})
			return
		}
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		TestingWriteProviderCompletionPage(w, definition.Name, item.AccountDisplay)
	}
}

func (s *SpacesService) CloudConnectionToken() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		item, err := s.database.CloudConnection(r.Context(), userID, chi.URLParam(r, "connectionID"))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		plaintext, err := s.decryptProviderSecret(item.Provider, item.CredentialCiphertext, item.CredentialNonce)
		var secret cloudOAuthSecret
		if err != nil || json.Unmarshal(plaintext, &secret) != nil || secret.Token.AccessToken == "" {
			writeSpaceError(w, errors.New("cloud credential is invalid"))
			return
		}
		if item.ExpiresAt != nil && item.ExpiresAt.Before(time.Now().UTC().Add(5*time.Minute)) {
			definition := TestingCloudOAuthCatalog[item.Provider]
			token, err := refreshCloudToken(r.Context(), definition, secret)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "cloud_reauthorization_required"})
				return
			}
			secret.Token = token
			encoded, _ := json.Marshal(secret)
			item.CredentialCiphertext, item.CredentialNonce, err = s.encryptProviderSecret(item.Provider, encoded)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			item.KeyVersion = s.keyVer
			if token.ExpiresIn > 0 {
				value := time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
				item.ExpiresAt = &value
			}
			if err := s.database.UpdateCloudConnectionCredential(r.Context(), *item); err != nil {
				writeSpaceError(w, err)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{
			"connection_id": item.ID, "provider": item.Provider,
			"access_token": secret.Token.AccessToken, "token_type": firstNonempty(secret.Token.TokenType, "Bearer"),
			"expires_at": item.ExpiresAt, "api_base": TestingCloudAPIBase(item.Provider),
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
		status, revokeErr := s.revokeCloudConnection(r.Context(), *item)
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
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
	if err != nil {
		return providerTokenEnvelope{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
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
	request, _ := http.NewRequestWithContext(ctx, method, endpoint, nil)
	request.Header.Set("Authorization", firstNonempty(token.TokenType, "Bearer")+" "+token.AccessToken)
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return "", ""
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", ""
	}
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	id := firstProviderString(value, "account_id", "id", "sub")
	name := firstProviderString(value, "name", "display_name", "displayName", "email", "userPrincipalName")
	return id, name
}

func TestingCloudCallbackURL(r *http.Request, provider string) string {
	base := configuredPublicAPIBase()
	if base == "" {
		scheme := "https"
		if r.TLS == nil && (strings.HasPrefix(r.Host, "localhost") || strings.HasPrefix(r.Host, "127.0.0.1")) {
			scheme = "http"
		}
		base = scheme + "://" + r.Host + requestAPIPathPrefix(r.URL.Path)
	}
	return base + "/oauth/cloud/" + url.PathEscape(provider) + "/callback"
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
