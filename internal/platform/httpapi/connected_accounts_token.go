package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"


	"github.com/go-chi/chi/v5"
)


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
