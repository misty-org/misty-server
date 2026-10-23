package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/kannachi323/misty/server/internal/accounts"
	"github.com/kannachi323/misty/server/internal/platform/db"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

// ChangePassword replaces the signed-in person's password (PUT /me/password).
// Both values are opaque strings to the server: the desktop app sends values it
// derived on the device, so the server never learns the password itself, and
// accounts that have not upgraded still send the password as typed. Wrong
// attempts count toward the same lockout as sign-in.
func ChangePassword(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := accounts.AuthenticatedUser(w, r)
		if !ok {
			return
		}
		var body struct {
			CurrentPassword string `json:"current_password"`
			NewPassword     string `json:"new_password"`
			// Credential: the new value is one the app derived on the device.
			Credential bool `json:"credential"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if body.CurrentPassword == "" || body.NewPassword == "" {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := accounts.ValidateNewPassword(body.NewPassword); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		user, err := database.GetUserByID(userID)
		if err != nil || user == nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if blocked, retryAfter := loginFailures.Blocked(user.Email, time.Now()); blocked {
			w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
			http.Error(w, "too many attempts; try again later", http.StatusTooManyRequests)
			return
		}
		// Other sessions end with the change; this request's own stays.
		keep := security.HashToken(accounts.SessionID(r))
		switch err := database.ChangePassword(userID, body.CurrentPassword, body.NewPassword, keep, body.Credential); {
		case err == nil:
			loginFailures.Succeed(user.Email)
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		case errors.Is(err, db.ErrWrongCurrentPassword):
			loginFailures.Fail(user.Email, time.Now())
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
		case errors.Is(err, db.ErrNoPasswordAccount):
			http.Error(w, "this account has no Misty password", http.StatusConflict)
		default:
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	}
}
