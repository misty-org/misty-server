package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
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
		definition, exists := TestingProviderOAuthCatalog[credential.Provider]
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

func exchangeProviderCode(ctx context.Context, definition providerOAuthDefinition, code, verifier, redirect string) (providerTokenEnvelope, []byte, error) {
	values := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "client_id": {TestingProviderOAuthClientID(definition)}, "client_secret": {TestingProviderOAuthClientSecret(definition)}}
	if definition.PKCE {
		values.Set("code_verifier", verifier)
	}
	var request *http.Request
	if definition.ID == "notion" {
		encoded, _ := json.Marshal(map[string]string{"grant_type": "authorization_code", "code": code, "redirect_uri": redirect})
		request, _ = http.NewRequestWithContext(ctx, http.MethodPost, definition.TokenURL, bytes.NewReader(encoded))
		request.Header.Set("Content-Type", "application/json")
		request.SetBasicAuth(values.Get("client_id"), values.Get("client_secret"))
	} else {
		request, _ = http.NewRequestWithContext(ctx, http.MethodPost, definition.TokenURL, strings.NewReader(values.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	request.Header.Set("Accept", "application/json")
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
	if err != nil {
		return providerTokenEnvelope{}, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return providerTokenEnvelope{}, nil, fmt.Errorf("token exchange returned %s", response.Status)
	}
	var token providerTokenEnvelope
	if err := json.Unmarshal(raw, &token); err != nil {
		return token, raw, err
	}
	return token, raw, nil
}

var providerCompletionPage = template.Must(template.New("provider-completion").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Provider}} connected</title>
  <style>
    :root { color-scheme: dark; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #090b0f; color: #f4f7fb; }
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: radial-gradient(circle at 50% 0%, #1b2635 0, #090b0f 42rem); }
    main { width: min(30rem, calc(100vw - 2rem)); padding: 2rem; border: 1px solid rgba(255,255,255,.12); border-radius: 1rem; background: rgba(15,18,24,.88); box-shadow: 0 24px 80px rgba(0,0,0,.36); text-align: center; }
    .mark { width: 3rem; height: 3rem; margin: 0 auto 1rem; display: grid; place-items: center; border-radius: 999px; background: #16a34a; color: white; font-size: 1.75rem; font-weight: 700; }
    h1 { margin: 0; font-size: 1.35rem; line-height: 1.2; }
    p { margin: .75rem 0 0; color: #aab4c3; line-height: 1.55; }
    strong { color: #f4f7fb; font-weight: 650; }
  </style>
</head>
<body>
  <main>
    <div class="mark">&check;</div>
    <h1>{{.Provider}} is connected</h1>
    <p>Misty saved <strong>{{.Account}}</strong>. Return to the Misty app to continue.</p>
    <p>You can close this browser tab.</p>
  </main>
</body>
</html>`))

func TestingWriteProviderCompletionPage(w http.ResponseWriter, providerName, accountName string) {
	if strings.TrimSpace(accountName) == "" {
		accountName = providerName + " account"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.WriteHeader(http.StatusOK)
	_ = providerCompletionPage.Execute(w, map[string]string{"Provider": providerName, "Account": accountName})
}

func refreshProviderToken(ctx context.Context, definition providerOAuthDefinition, refreshToken string) (providerTokenEnvelope, []byte, error) {
	values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "client_id": {TestingProviderOAuthClientID(definition)}, "client_secret": {TestingProviderOAuthClientSecret(definition)}}
	var request *http.Request
	if definition.ID == "notion" {
		encoded, _ := json.Marshal(map[string]string{"grant_type": "refresh_token", "refresh_token": refreshToken})
		request, _ = http.NewRequestWithContext(ctx, http.MethodPost, definition.TokenURL, bytes.NewReader(encoded))
		request.Header.Set("Content-Type", "application/json")
		request.SetBasicAuth(values.Get("client_id"), values.Get("client_secret"))
	} else {
		request, _ = http.NewRequestWithContext(ctx, http.MethodPost, definition.TokenURL, strings.NewReader(values.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
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

func fetchProviderAccountIdentity(ctx context.Context, provider string, token providerTokenEnvelope) (string, string) {
	endpoint, method := "", http.MethodGet
	switch provider {
	case "google":
		endpoint = "https://openidconnect.googleapis.com/v1/userinfo"
	case "discord":
		endpoint = "https://discord.com/api/v10/users/@me"
	default:
		return "", ""
	}
	request, _ := http.NewRequestWithContext(ctx, method, endpoint, nil)
	kind := token.TokenType
	if kind == "" {
		kind = "Bearer"
	}
	request.Header.Set("Authorization", kind+" "+token.AccessToken)
	request.Header.Set("Accept", "application/json")
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
	id := firstProviderString(value, "id", "account_id", "sub")
	name := firstProviderString(value, "displayName", "name", "email", "login", "userPrincipalName")
	return id, name
}

func firstProviderString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := value[key].(string); ok && text != "" {
			return text
		}
	}
	return ""
}

func providerAccountIdentity(provider string, token providerTokenEnvelope, raw []byte) (string, string) {
	if token.Team != nil {
		return token.Team.ID, token.Team.Name
	}
	if token.WorkspaceID != "" {
		return token.WorkspaceID, token.WorkspaceName
	}
	var values map[string]any
	_ = json.Unmarshal(raw, &values)
	for _, pair := range [][2]string{{"account_id", "account_name"}, {"user_id", "user_name"}, {"guild_id", "guild_name"}} {
		id, _ := values[pair[0]].(string)
		name, _ := values[pair[1]].(string)
		if id != "" {
			return id, name
		}
	}
	return "", ""
}

func TestingProviderCallbackURL(r *http.Request, provider string) string {
	base := configuredPublicAPIBase()
	if base == "" {
		scheme := "https"
		if r.TLS == nil && (strings.HasPrefix(r.Host, "localhost") || strings.HasPrefix(r.Host, "127.0.0.1")) {
			scheme = "http"
		}
		base = scheme + "://" + r.Host + requestAPIPathPrefix(r.URL.Path)
	}
	return base + "/oauth/providers/" + url.PathEscape(provider) + "/callback"
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
