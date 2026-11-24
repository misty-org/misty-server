package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
	"github.com/misty-org/misty-server/internal/spaces"
)

func ExchangeIntegrationToken(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Token string `json:"token"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		token := strings.TrimSpace(body.Token)
		if !spaces.ValidIntegrationToken(token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_token"})
			return
		}
		integration, err := database.SpaceIntegrationByToken(r.Context(), spaces.HashIntegrationToken(token))
		if errors.Is(err, db.ErrSpaceNotFound) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_token"})
			return
		}
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		signer, err := security.SessionSignerFromEnv()
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "unavailable"})
			return
		}
		access, err := signer.MintIntegration(security.IntegrationClaims{
			Subject: integration.ID, SpaceID: integration.SpaceID, Access: integration.Access,
			KeyPrefix: integration.KeyPrefix, Name: integration.Name,
		}, time.Now().Add(security.IntegrationTokenTTL))
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "unavailable"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": access,
			"expires_in":   int(security.IntegrationTokenTTL.Seconds()),
			"integration": map[string]string{
				"id":       integration.ID,
				"space_id": integration.SpaceID,
				"name":     integration.Name,
			},
		})
	}
}
