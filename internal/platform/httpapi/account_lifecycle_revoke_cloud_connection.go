package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

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
		token := firstNonempty(secret.Token.RefreshToken, secret.Token.AccessToken)
		if token == "" {
			return "no_token_local_revocation", nil
		}
		if err := TestingPostProviderRevocation(
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
		if err := TestingPostProviderRevocation(
			ctx,
			"https://api.dropboxapi.com/2/auth/token/revoke",
			nil,
			secret.Token.AccessToken,
		); err != nil {
			return "revocation_failed", fmt.Errorf("revoke Dropbox credential: %w", err)
		}
		return "revoked", nil
	case "onedrive":
		// Microsoft does not expose a per-refresh-token revocation endpoint
		// for this delegated desktop flow. Erasing Misty's encrypted copy is
		// the supported revocation action; users can also remove Misty from
		// their Microsoft Account consent page.
		return "local_revocation", nil
	default:
		return "unknown_provider_local_revocation", nil
	}
}

func TestingPostProviderRevocation(
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
