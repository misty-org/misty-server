package api

import (
	"net/http"

	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
)

func WebHandoff(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		code, err := security.GenerateSecureToken()
		if err == nil {
			err = database.CreateWebHandoff(r.Context(), userID, security.HashToken(code))
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"code": code})
	}
}

func RedeemWebHandoff(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var body struct {
			Code string `json:"code"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if body.Code == "" || len(body.Code) > 256 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "handoff_invalid"})
			return
		}
		userID, err := database.ConsumeWebHandoff(r.Context(), security.HashToken(body.Code))
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		var user *db.User
		if userID != "" {
			user, err = database.GetUserByID(userID)
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if user == nil {
			writeJSON(w, http.StatusGone, map[string]string{"code": "handoff_invalid"})
			return
		}
		writeAuthSession(w, r, database, user, http.StatusOK)
	}
}
