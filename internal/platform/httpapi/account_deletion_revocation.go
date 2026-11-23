package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
)

type providerTokenEnvelope struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

type cloudOAuthSecret struct {
	Token providerTokenEnvelope
}

func (s *SpacesService) revokeAccountProviders(
	ctx context.Context, userID string,
) (map[string]string, error) {
	connections, err := s.database.AccountDeletionConnections(ctx, userID)
	if err != nil {
		return nil, err
	}
	status := map[string]string{}
	for _, connection := range connections {
		key := connection.Provider + ":" + connection.ID
		connectionStatus, revokeErr := s.revokeCloudConnection(ctx, connection)
		status[key] = connectionStatus
		if revokeErr != nil {
			return status, revokeErr
		}
	}
	providerCredentials, err := s.database.AccountDeletionProviderCredentials(ctx, userID)
	if err != nil {
		return status, err
	}
	for _, credential := range providerCredentials {
		key := "integration:" + credential.Provider + ":" + credential.ID
		connectionStatus, revokeErr := s.revokeProviderCredential(ctx, credential)
		status[key] = connectionStatus
		if revokeErr != nil {
			return status, revokeErr
		}
	}
	return status, nil
}

func (s *SpacesService) revokeProviderCredential(
	ctx context.Context, credential db.ProviderCredential,
) (string, error) {
	plaintext, decryptErr := s.decryptProviderSecret(
		credential.Provider, credential.Ciphertext, credential.Nonce,
	)
	var secret cloudOAuthSecret
	if decryptErr != nil || json.Unmarshal(plaintext, &secret) != nil {
		return "credential_unreadable_local_revocation", nil
	}
	if credential.Provider != "google" {
		return "local_revocation", nil
	}
	token := firstNonEmpty(secret.Token.RefreshToken, secret.Token.AccessToken)
	if token == "" {
		return "no_token_local_revocation", nil
	}
	if err := PostProviderRevocation(
		ctx, "https://oauth2.googleapis.com/revoke",
		url.Values{"token": {token}}, "",
	); err != nil {
		return "revocation_failed", fmt.Errorf("revoke Google integration credential: %w", err)
	}
	return "revoked", nil
}

func (s *SpacesService) revokeCloudConnection(
	ctx context.Context, connection db.CloudConnection,
) (string, error) {
	plaintext, decryptErr := s.decryptProviderSecret(
		connection.Provider,
		connection.CredentialCiphertext,
		connection.CredentialNonce,
	)
	var secret cloudOAuthSecret
	if decryptErr != nil || json.Unmarshal(plaintext, &secret) != nil {
		return "credential_unreadable_local_revocation", nil
	}
	switch connection.Provider {
	case "drive":
		token := firstNonEmpty(secret.Token.RefreshToken, secret.Token.AccessToken)
		if token == "" {
			return "no_token_local_revocation", nil
		}
		if err := PostProviderRevocation(
			ctx,
			"https://oauth2.googleapis.com/revoke",
			url.Values{"token": {token}},
			"",
		); err != nil {
			return "revocation_failed", fmt.Errorf("revoke Google credential: %w", err)
		}
		return "revoked", nil
	case "dropbox":
		if secret.Token.AccessToken == "" {
			return "no_token_local_revocation", nil
		}
		if err := PostProviderRevocation(
			ctx,
			"https://api.dropboxapi.com/2/auth/token/revoke",
			nil,
			secret.Token.AccessToken,
		); err != nil {
			return "revocation_failed", fmt.Errorf("revoke Dropbox credential: %w", err)
		}
		return "revoked", nil
	case "onedrive":
		return "local_revocation", nil
	default:
		return "unknown_provider_local_revocation", nil
	}
}

func (s *SpacesService) decryptProviderSecret(provider string, ciphertext, nonce []byte) ([]byte, error) {
	plaintext, err := s.aead.Open(nil, nonce, ciphertext, []byte("misty-provider-v2:"+provider))
	if err == nil || provider != "google" {
		return plaintext, err
	}
	return s.aead.Open(nil, nonce, ciphertext, []byte("misty-provider-v2:google_calendar"))
}

func PostProviderRevocation(
	ctx context.Context, endpoint string, values url.Values, bearer string,
) error {
	body := ""
	if values != nil {
		body = values.Encode()
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, endpoint, strings.NewReader(body),
	)
	if err != nil {
		return err
	}
	if values != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("provider returned %s", response.Status)
	}
	return nil
}
