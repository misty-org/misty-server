package api

import (
	"encoding/json"
	"net/http"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
)

// BindConnectedAccountToSpaceProvider lets a space reuse an account-level
// authorization. It deliberately re-encrypts the token for the legacy space
// runtime, so the two storage domains do not share ciphertext or AAD.
func (s *SpacesService) BindConnectedAccountToSpaceProvider() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		provider, spaceID := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "provider"))), chi.URLParam(r, "spaceID")
		var body struct {
			ConnectionID string `json:"connection_id"`
			Capability   string `json:"capability"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		allowed, err := s.database.HasSpacePermission(r.Context(), userID, spaceID, db.PermissionIntegrationsManage)
		if err != nil || !allowed {
			writeSpaceError(w, db.ErrSpaceForbidden)
			return
		}
		if body.Capability == "" {
			body.Capability = "calendar_read"
		}
		account, err := s.database.ConnectedAccount(r.Context(), userID, body.ConnectionID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if !connectedAccountCanBind(*account, provider, body.Capability) {
			writeSpaceError(w, db.ErrSpaceForbidden)
			return
		}
		plaintext, err := s.decryptConnectedAccountSecret(provider, account.CredentialCiphertext, account.CredentialNonce)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		var token providerTokenEnvelope
		if json.Unmarshal(plaintext, &token) != nil || strings.TrimSpace(token.AccessToken) == "" {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		ciphertext, nonce, err := s.encryptProviderSecret(provider, plaintext)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		item, err := s.database.SaveProviderCredential(r.Context(), db.ProviderCredential{
			SpaceID: spaceID, UserID: userID, Provider: provider,
			Ciphertext: ciphertext, Nonce: nonce, KeyVersion: s.keyVer,
			AccountID: account.AccountID, AccountDisplay: account.AccountDisplay,
			ExpiresAt: account.ExpiresAt,
		}, account.AccountDisplay, account.GrantedScopes)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"integration": item, "connection_id": account.ID,
			"capability": strings.ToLower(strings.TrimSpace(body.Capability)),
		})
	}
}

func connectedAccountCanBind(account db.ConnectedAccount, provider, capability string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	capability = strings.ToLower(strings.TrimSpace(capability))
	if provider != "google" || account.Provider != provider || account.Status != "active" || account.RevokedAt != nil {
		return false
	}
	if capability != "calendar_read" && capability != "calendar_write" {
		return false
	}
	return containsString(account.Capabilities, capability)
}

func TestingConnectedAccountCanBind(account db.ConnectedAccount, provider, capability string) bool {
	return connectedAccountCanBind(account, provider, capability)
}
