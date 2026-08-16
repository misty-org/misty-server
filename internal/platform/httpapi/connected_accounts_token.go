package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
)

// connectedAccountAccessTokenForCapability is the server-side token broker for
// tool adapters. It verifies both ownership and the capability granted during
// consent. Long-lived account credentials never cross the API boundary.
func (s *SpacesService) connectedAccountAccessTokenForCapability(ctx context.Context, userID, connectionID, capability string) (string, string, error) {
	item, err := s.database.ConnectedAccount(ctx, userID, connectionID)
	if err != nil {
		return "", "", err
	}
	if item.Status != "active" || item.RevokedAt != nil {
		return "", "", db.ErrSpaceForbidden
	}
	if capability = strings.ToLower(strings.TrimSpace(capability)); capability == "" || !containsString(item.Capabilities, capability) {
		return "", "", db.ErrSpaceForbidden
	}
	plaintext, err := s.decryptConnectedAccountSecret(item.Provider, item.CredentialCiphertext, item.CredentialNonce)
	if err != nil {
		_ = s.database.SetConnectedAccountHealth(ctx, userID, item.ID, "needs_attention", "credential_invalid")
		return "", "", errors.New("connected account credential is invalid")
	}
	var token providerTokenEnvelope
	if json.Unmarshal(plaintext, &token) != nil || token.AccessToken == "" {
		_ = s.database.SetConnectedAccountHealth(ctx, userID, item.ID, "needs_attention", "credential_invalid")
		return "", "", errors.New("connected account credential is invalid")
	}
	if item.ExpiresAt == nil || item.ExpiresAt.After(time.Now().UTC().Add(5*time.Minute)) {
		return token.AccessToken, firstNonempty(token.TokenType, "Bearer"), nil
	}
	definition, exists := TestingConnectedAccountOAuthCatalog[item.Provider]
	if !exists || token.RefreshToken == "" {
		_ = s.database.SetConnectedAccountHealth(ctx, userID, item.ID, "needs_attention", "reauthorization_required")
		return "", "", errors.New("connected account requires reauthorization")
	}
	refreshed, err := refreshConnectedAccountToken(ctx, definition, token.RefreshToken)
	if err != nil {
		_ = s.database.SetConnectedAccountHealth(ctx, userID, item.ID, "needs_attention", "refresh_failed")
		return "", "", err
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = token.RefreshToken
	}
	encoded, _ := json.Marshal(refreshed)
	item.CredentialCiphertext, item.CredentialNonce, err = s.encryptConnectedAccountSecret(item.Provider, encoded)
	if err != nil {
		return "", "", err
	}
	item.KeyVersion = s.keyVer
	if refreshed.ExpiresIn > 0 {
		value := time.Now().UTC().Add(time.Duration(refreshed.ExpiresIn) * time.Second)
		item.ExpiresAt = &value
	}
	if err := s.database.UpdateConnectedAccountCredential(ctx, *item); err != nil {
		return "", "", err
	}
	return refreshed.AccessToken, firstNonempty(refreshed.TokenType, "Bearer"), nil
}

// TestingEncryptConnectedAccountAccessToken creates an encrypted credential
// fixture without exposing production token-decryption paths to API callers.
func (s *SpacesService) TestingEncryptConnectedAccountAccessToken(provider, accessToken string) ([]byte, []byte, error) {
	encoded, err := json.Marshal(providerTokenEnvelope{
		AccessToken:  strings.TrimSpace(accessToken),
		RefreshToken: "testing-refresh-token",
		TokenType:    "Bearer",
	})
	if err != nil {
		return nil, nil, err
	}
	return s.encryptConnectedAccountSecret(provider, encoded)
}

func (s *SpacesService) DeleteConnectedAccount() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		connectionID := chi.URLParam(r, "connectionID")
		item, err := s.database.ConnectedAccount(r.Context(), userID, connectionID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		revocation := "local_credentials_erased"
		if plaintext, decryptErr := s.decryptConnectedAccountSecret(item.Provider, item.CredentialCiphertext, item.CredentialNonce); decryptErr == nil {
			var token providerTokenEnvelope
			if json.Unmarshal(plaintext, &token) == nil && token.AccessToken != "" {
				if item.Provider == "figma" {
					bindings, _ := s.database.FigmaBindingsForConnection(r.Context(), userID, connectionID)
					provider := s.figmaProvider(token.AccessToken)
					for _, binding := range bindings {
						subscriptions, _ := s.database.FigmaWebhookSubscriptions(r.Context(), binding.ID)
						for _, subscription := range subscriptions {
							_ = provider.DeleteWebhook(r.Context(), subscription.WebhookID)
						}
					}
				}
				if item.Provider == "google" {
					if revokeConnectedGoogleAccount(r.Context(), token.AccessToken) == nil {
						revocation = "provider_revoked"
					} else {
						revocation = "provider_revocation_failed_local_credentials_erased"
					}
				} else {
					revocation = "provider_session_not_revocable_local_credentials_erased"
				}
			}
		}
		if item.Provider == "figma" {
			if cleanupErr := s.database.DisableFigmaBindingsForConnection(r.Context(), userID, connectionID); cleanupErr != nil {
				writeSpaceError(w, cleanupErr)
				return
			}
		}
		if err := s.database.RevokeConnectedAccount(r.Context(), userID, connectionID); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.Header().Set("X-Misty-Provider-Revocation", revocation)
		w.WriteHeader(http.StatusNoContent)
	}
}

func revokeConnectedGoogleAccount(ctx context.Context, token string) error {
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/revoke",
		strings.NewReader(url.Values{"token": {token}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("provider token revocation failed")
	}
	return nil
}
