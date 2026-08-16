package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const cloudHandoffLifetime = 60 * time.Second

func (s *SpacesService) BindConnectedAccountCloudConnection() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			ConnectionID string `json:"connection_id"`
			Name         string `json:"name"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		body.Name = strings.TrimSpace(body.Name)
		account, err := s.database.ConnectedAccount(r.Context(), userID, body.ConnectionID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		provider, valid := cloudProviderForConnectedAccount(*account)
		if body.Name == "" || !valid || account.Status != "active" || !containsString(account.Capabilities, "files") {
			writeSpaceError(w, db.ErrSpaceForbidden)
			return
		}
		entitlements, err := s.database.EntitlementsForUser(r.Context(), userID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		maximum := 0
		if entitlements.Plan == db.TierBasic {
			maximum = 1
		}
		item, err := s.database.BindConnectedAccountCloudConnection(r.Context(), userID, *account, provider, body.Name, maximum)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, cloudConnectionJSON(*item))
	}
}

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

func (s *SpacesService) CloudConnectionHandoff() http.HandlerFunc {
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
		if item.Status != "active" {
			writeJSON(w, http.StatusConflict, map[string]string{"code": "cloud_reauthorization_required"})
			return
		}
		handoff := randomProviderValue(32)
		expiresAt := time.Now().UTC().Add(cloudHandoffLifetime)
		if err := s.database.CreateCloudCredentialHandoff(r.Context(), hashProviderValue(handoff), db.CloudCredentialHandoff{
			UserID: userID, CloudConnectionID: item.ID, ExpiresAt: expiresAt,
		}); err != nil {
			writeSpaceError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, map[string]any{
			"connection_id": item.ID, "provider": item.Provider, "handoff": handoff,
			"redeem_url": requestPublicAPIBase(r) + "/cloud/handoffs/redeem", "expires_at": expiresAt,
		})
	}
}

func (s *SpacesService) RedeemCloudConnectionHandoff() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Handoff string `json:"handoff"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		body.Handoff = strings.TrimSpace(body.Handoff)
		if body.Handoff == "" {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		claim, err := s.database.ConsumeCloudCredentialHandoff(r.Context(), hashProviderValue(body.Handoff))
		if err != nil {
			writeJSON(w, http.StatusGone, map[string]string{"code": "cloud_handoff_expired"})
			return
		}
		item, err := s.database.CloudConnection(r.Context(), claim.UserID, claim.CloudConnectionID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		token, tokenType, err := s.cloudConnectionAccessToken(r.Context(), claim.UserID, item)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "cloud_reauthorization_required"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{
			"connection_id": item.ID, "provider": item.Provider,
			"access_token": token, "token_type": tokenType, "expires_at": item.ExpiresAt,
		})
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
