package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	envconfig "github.com/kannachi323/misty/server/internal/platform/config"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type cloudOAuthDefinition struct {
	ID, Name, AuthorizeURL, TokenURL, ClientIDEnv, ClientSecretEnv string
	Scopes                                                         []string
	PKCE                                                           bool
}

var TestingCloudOAuthCatalog = map[string]cloudOAuthDefinition{
	"drive": {
		ID: "drive", Name: "Google Drive",
		AuthorizeURL: "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:     "https://oauth2.googleapis.com/token",
		ClientIDEnv:  "MISTY_GOOGLE_DRIVE_CLIENT_ID", ClientSecretEnv: "MISTY_GOOGLE_DRIVE_CLIENT_SECRET",
		Scopes: []string{"openid", "email", "profile", "https://www.googleapis.com/auth/drive"},
		PKCE:   true,
	},
	"dropbox": {
		ID: "dropbox", Name: "Dropbox",
		AuthorizeURL: "https://www.dropbox.com/oauth2/authorize",
		TokenURL:     "https://api.dropboxapi.com/oauth2/token",
		ClientIDEnv:  "MISTY_DROPBOX_CLIENT_ID", ClientSecretEnv: "MISTY_DROPBOX_CLIENT_SECRET",
		PKCE: true,
	},
	"onedrive": {
		ID: "onedrive", Name: "Microsoft OneDrive",
		AuthorizeURL: "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
		TokenURL:     "https://login.microsoftonline.com/common/oauth2/v2.0/token",
		ClientIDEnv:  "MISTY_ONEDRIVE_CLIENT_ID", ClientSecretEnv: "MISTY_ONEDRIVE_CLIENT_SECRET",
		Scopes: []string{"offline_access", "User.Read", "Files.ReadWrite.All"},
		PKCE:   true,
	},
}

type cloudOAuthSecret struct {
	Verifier, ClientID, ClientSecret string
	Custom                           bool
	Token                            providerTokenEnvelope
}

func (s *SpacesService) CloudConnections() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		items, err := s.database.CloudConnections(r.Context(), userID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		result := make([]map[string]any, 0, len(items))
		for _, item := range items {
			result = append(result, cloudConnectionJSON(item))
		}
		entitlements, err := s.database.EntitlementsForUser(r.Context(), userID)
		if err != nil {
			writeSpaceError(w, errors.New("account license is unavailable"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"connections": result,
			"limit": map[string]any{
				"used": len(result),
				"maximum": func() any {
					if entitlements.Plan != db.TierBasic {
						return nil
					}
					return 1
				}(),
			},
		})
	}
}

func cloudConnectionJSON(item db.CloudConnection) map[string]any {
	source := "legacy_cloud"
	if item.ConnectedAccountID != "" {
		source = "connected_account"
	}
	return map[string]any{
		"id": item.ID, "provider": item.Provider, "name": item.Name,
		"account_id": item.AccountID, "account_display": item.AccountDisplay,
		"uses_custom_oauth_client": item.UsesCustomOAuthClient,
		"connected_account_id":     item.ConnectedAccountID, "connection_source": source,
		"status": item.Status, "last_error_code": item.LastErrorCode,
		"expires_at": item.ExpiresAt, "created_at": item.CreatedAt, "updated_at": item.UpdatedAt,
	}
}

func TestingCloudConnectionJSON(item db.CloudConnection) map[string]any {
	return cloudConnectionJSON(item)
}

func (s *SpacesService) BeginCloudAuthorization() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		provider := chi.URLParam(r, "provider")
		definition, exists := TestingCloudOAuthCatalog[provider]
		if !exists {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "cloud_provider_invalid"})
			return
		}
		var body struct {
			Name         string `json:"name"`
			ClientID     string `json:"clientID"`
			ClientSecret string `json:"clientSecret"`
			ReturnTo     string `json:"returnTo"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		body.Name, body.ClientID, body.ClientSecret = strings.TrimSpace(body.Name), strings.TrimSpace(body.ClientID), strings.TrimSpace(body.ClientSecret)
		if body.Name == "" || !TestingValidProviderReturnPath(body.ReturnTo) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "cloud_connection_invalid"})
			return
		}
		if (body.ClientID == "") != (body.ClientSecret == "") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "oauth_client_pair_required"})
			return
		}
		clientID, clientSecret, custom := body.ClientID, body.ClientSecret, body.ClientID != ""
		if !custom {
			clientID, clientSecret = strings.TrimSpace(envconfig.Getenv(definition.ClientIDEnv)), strings.TrimSpace(envconfig.Getenv(definition.ClientSecretEnv))
		}
		if clientID == "" || clientSecret == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "cloud_provider_not_configured"})
			return
		}
		connections, err := s.database.CloudConnections(r.Context(), userID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		entitlements, err := s.database.EntitlementsForUser(r.Context(), userID)
		if err != nil {
			writeSpaceError(w, errors.New("account license is unavailable"))
			return
		}
		replacingExisting := false
		for _, connection := range connections {
			if connection.Name == body.Name {
				replacingExisting = true
				break
			}
		}
		if entitlements.Plan == db.TierBasic && len(connections) >= 1 && !replacingExisting {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "cloud_connection_limit", "message": "Basic accounts can connect one cloud account."})
			return
		}

		state, verifier := randomProviderValue(32), randomProviderValue(48)
		secret, _ := json.Marshal(cloudOAuthSecret{Verifier: verifier, ClientID: clientID, ClientSecret: clientSecret, Custom: custom})
		ciphertext, nonce, err := s.encryptProviderSecret(provider, secret)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		expires := time.Now().UTC().Add(10 * time.Minute)
		if err := s.database.CreateCloudOAuthState(r.Context(), hashProviderValue(state), db.CloudOAuthState{
			UserID: userID, Provider: provider, ConnectionName: body.Name,
			SecretCiphertext: ciphertext, SecretNonce: nonce, ReturnTo: body.ReturnTo, ExpiresAt: expires,
		}); err != nil {
			writeSpaceError(w, err)
			return
		}
		callback := TestingCloudCallbackURL(r, provider)
		params := url.Values{
			"client_id": {clientID}, "redirect_uri": {callback}, "response_type": {"code"}, "state": {state},
		}
		if len(definition.Scopes) > 0 {
			params.Set("scope", strings.Join(definition.Scopes, " "))
		}
		if definition.PKCE {
			sum := sha256Bytes(verifier)
			params.Set("code_challenge", sum)
			params.Set("code_challenge_method", "S256")
		}
		switch provider {
		case "drive":
			params.Set("access_type", "offline")
			params.Set("prompt", "consent")
		case "dropbox":
			params.Set("token_access_type", "offline")
		}
		writeJSON(w, http.StatusOK, map[string]any{"provider": provider, "authorization_url": definition.AuthorizeURL + "?" + params.Encode(), "state_expires_at": expires})
	}
}

func sha256Bytes(value string) string {
	sum := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
