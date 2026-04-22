package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/kannachi323/misty/server/core/license"
	"github.com/kannachi323/misty/server/db"
)

func extractLicenseToken(r *http.Request) string {
	if token := r.Header.Get("X-License-Token"); token != "" {
		return token
	}

	authHeader := r.Header.Get("Authorization")
	const bearerPrefix = "Bearer "
	if strings.HasPrefix(authHeader, bearerPrefix) {
		return strings.TrimSpace(strings.TrimPrefix(authHeader, bearerPrefix))
	}

	return ""
}

func GetSubscription(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractLicenseToken(r)
		if token == "" {
			http.Error(w, "missing license token", http.StatusUnauthorized)
			return
		}

		claims, err := license.Validate(token)
		if err != nil {
			http.Error(w, "invalid license token", http.StatusUnauthorized)
			return
		}

		sub, err := database.GetSubscription(claims.UserID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"user_id":    claims.UserID,
			"email":      claims.Email,
			"tier":       string(sub.Tier),
			"status":     sub.Status,
			"expires_at": sub.ExpiresAt,
		})
	}
}
