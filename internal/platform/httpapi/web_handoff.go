package api

import (
	"net/http"

	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
)

// The app hands its account to the website: POST /me/web-handoff gives the signed-in app a
// single-use code that lasts two minutes, the app opens the website with it in the URL's
// fragment (which never reaches a server or a referrer), and the website trades it at
// POST /auth/web-handoff for a session of its own. A code only ever starts a session for the
// account that asked for it.

// WebHandoff answers POST /me/web-handoff with a new code for the signed-in account.
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

// RedeemWebHandoff answers POST /auth/web-handoff {"code"} with a session for the code's account.
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
