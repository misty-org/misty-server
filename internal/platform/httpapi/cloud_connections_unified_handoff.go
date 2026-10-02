package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const cloudHandoffLifetime = 60 * time.Second

func cloudProviderForConnectedAccount(account db.ConnectedAccount) (string, bool) {
	switch account.Provider {
	case "google":
		return "drive", true
	case "microsoft":
		return "onedrive", true
	case "dropbox":
		return "dropbox", true
	default:
		return "", false
	}
}

func (s *SpacesService) cloudConnectionAccessToken(ctx context.Context, userID string, item *db.CloudConnection) (string, string, error) {
	if item == nil {
		return "", "", db.ErrSpaceInvalid
	}
	if item.ConnectedAccountID != "" {
		return s.connectedAccountAccessTokenForCapability(ctx, userID, item.ConnectedAccountID, "files")
	}
	plaintext, err := s.decryptProviderSecret(item.Provider, item.CredentialCiphertext, item.CredentialNonce)
	var secret cloudOAuthSecret
	if err != nil || json.Unmarshal(plaintext, &secret) != nil || secret.Token.AccessToken == "" {
		return "", "", errors.New("cloud credential is invalid")
	}
	if item.ExpiresAt != nil && item.ExpiresAt.Before(time.Now().UTC().Add(5*time.Minute)) {
		definition, exists := TestingCloudOAuthCatalog[item.Provider]
		if !exists {
			return "", "", errors.New("cloud provider is unavailable")
		}
		token, refreshErr := refreshCloudToken(ctx, definition, secret)
		if refreshErr != nil {
			return "", "", refreshErr
		}
		secret.Token = token
		encoded, _ := json.Marshal(secret)
		item.CredentialCiphertext, item.CredentialNonce, err = s.encryptProviderSecret(item.Provider, encoded)
		if err != nil {
			return "", "", err
		}
		item.KeyVersion = s.keyVer
		if token.ExpiresIn > 0 {
			value := time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
			item.ExpiresAt = &value
		}
		if err := s.database.UpdateCloudConnectionCredential(ctx, *item); err != nil {
			return "", "", err
		}
	}
	return secret.Token.AccessToken, firstNonempty(secret.Token.TokenType, "Bearer"), nil
}

func (s *SpacesService) TestingEncryptLegacyCloudAccessToken(provider, accessToken string) ([]byte, []byte, error) {
	raw, _ := json.Marshal(cloudOAuthSecret{Token: providerTokenEnvelope{AccessToken: accessToken, TokenType: "Bearer"}})
	return s.encryptProviderSecret(provider, raw)
}

func (s *SpacesService) TestingCloudConnectionAccessToken(ctx context.Context, userID string, item *db.CloudConnection) (string, string, error) {
	return s.cloudConnectionAccessToken(ctx, userID, item)
}

func TestingCloudProviderForConnectedAccount(provider string) (string, bool) {
	return cloudProviderForConnectedAccount(db.ConnectedAccount{Provider: provider})
}
