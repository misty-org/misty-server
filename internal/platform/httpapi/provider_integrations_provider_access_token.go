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

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

func (s *SpacesService) providerAccessToken(ctx context.Context, userID, spaceID, integrationID string) (string, string, error) {
	credential, err := s.database.ProviderCredential(ctx, userID, spaceID, integrationID)
	if err != nil {
		return "", "", err
	}
	plaintext, err := s.decryptProviderSecret(credential.Provider, credential.Ciphertext, credential.Nonce)
	if err != nil {
		return "", "", err
	}
	var token providerTokenEnvelope
	if json.Unmarshal(plaintext, &token) != nil || token.AccessToken == "" {
		return "", "", errors.New("provider credential is invalid")
	}
	if credential.ExpiresAt != nil && credential.ExpiresAt.Before(time.Now().UTC().Add(5*time.Minute)) {
		if token.RefreshToken == "" {
			return "", "", errors.New("provider connection requires reauthorization")
		}
		definition, exists := providerRefreshDefinition(credential.Provider)
		if !exists {
			return "", "", errors.New("provider configuration is missing")
		}
		refreshed, raw, refreshErr := refreshProviderToken(ctx, definition, token.RefreshToken)
		if refreshErr != nil {
			return "", "", refreshErr
		}
		if refreshed.RefreshToken == "" {
			refreshed.RefreshToken = token.RefreshToken
			raw, _ = json.Marshal(refreshed)
		}
		ciphertext, nonce, sealErr := s.encryptProviderSecret(credential.Provider, raw)
		if sealErr != nil {
			return "", "", sealErr
		}
		credential.Ciphertext, credential.Nonce, credential.KeyVersion = ciphertext, nonce, s.keyVer
		if refreshed.ExpiresIn > 0 {
			value := time.Now().UTC().Add(time.Duration(refreshed.ExpiresIn) * time.Second)
			credential.ExpiresAt = &value
		}
		if updateErr := s.database.UpdateProviderCredentialSecret(ctx, *credential); updateErr != nil {
			return "", "", updateErr
		}
		token = refreshed
	}
	return token.AccessToken, token.TokenType, nil
}

func providerRefreshDefinition(provider string) (providerOAuthDefinition, bool) {
	connected, exists := TestingConnectedAccountOAuthCatalog[provider]
	if !exists {
		return providerOAuthDefinition{}, false
	}
	return providerOAuthDefinition{
		ID: connected.ID, Name: connected.Name,
		AuthorizeURL: connected.AuthorizeURL, TokenURL: connected.TokenURL,
		ClientIDEnv: connected.ClientIDEnv, ClientSecretEnv: connected.ClientSecretEnv,
		PKCE: true,
	}, true
}


func refreshProviderToken(ctx context.Context, definition providerOAuthDefinition, refreshToken string) (providerTokenEnvelope, []byte, error) {
	values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "client_id": {TestingProviderOAuthClientID(definition)}, "client_secret": {TestingProviderOAuthClientSecret(definition)}}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, definition.TokenURL, strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
	if err != nil {
		return providerTokenEnvelope{}, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return providerTokenEnvelope{}, nil, fmt.Errorf("token refresh returned %s", response.Status)
	}
	var token providerTokenEnvelope
	if err := json.Unmarshal(raw, &token); err != nil {
		return token, raw, err
	}
	if token.AccessToken == "" {
		return token, raw, errors.New("token refresh did not return access token")
	}
	return token, raw, nil
}


func firstProviderString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := value[key].(string); ok && text != "" {
			return text
		}
	}
	return ""
}


func configuredPublicAPIBase() string {
	base := strings.TrimRight(strings.TrimSpace(envconfig.Getenv("MISTY_PUBLIC_API_URL")), "/")
	if base == "" {
		return ""
	}
	parsed, err := url.Parse(base)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" && (parsed.Path == "" || parsed.Path == "/") {
		// Compatibility for existing origin-only deployments. New configuration
		// should always include the complete API path explicitly.
		return base + "/api"
	}
	return base
}
