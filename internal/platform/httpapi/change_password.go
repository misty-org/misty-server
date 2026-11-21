package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/misty-org/misty-server/internal/accounts"
	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
)

// ChangePassword replaces the signed-in person's password (PUT /me/password).
// Wrong attempts count toward the same lockout as sign-in. reset_token, from a
// setup key's sign-in, stands in for the current password: that is how a
// forgotten password is reset.
func ChangePassword(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := accounts.AuthenticatedUser(w, r)
		if !ok {
			return
		}
		var body struct {
			CurrentPassword string `json:"current_password"`
			NewPassword     string `json:"new_password"`
			// ResetToken: password_reset_token from POST /setup-keys/redeem.
			ResetToken string `json:"reset_token"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		// current_password may be empty only for the first password of an
		// account a setup key signed in (it has none yet); the store checks.
		if body.NewPassword == "" {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := accounts.ValidateNewPassword(body.NewPassword); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := checkNewPasswordBreached(r.Context(), body.NewPassword); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		user, err := database.GetUserByID(userID)
		if err != nil || user == nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		failures := passwordFailureCaps(r, user.Email)
		if lockedOut(w, r, database, failures, "too many attempts; try again later") {
			return
		}
		// Other sessions end with the change; this request's own stays.
		keep := security.HashToken(accounts.SessionID(r))
		if body.ResetToken != "" {
			err = database.ResetPassword(r.Context(), userID, security.HashToken(body.ResetToken), body.NewPassword, keep, time.Now())
		} else {
			err = database.ChangePassword(userID, body.CurrentPassword, body.NewPassword, keep)
		}
		switch {
		case err == nil:
			clearFailures(r, database, failures)
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		case errors.Is(err, db.ErrWrongCurrentPassword):
			recordFailure(r, database, failures)
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
		case errors.Is(err, db.ErrPasswordResetTokenInvalid):
			recordFailure(r, database, failures)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "password_reset_expired", "message": "This sign-in can no longer reset the password. Ask for a new sign-in link."})
		case errors.Is(err, db.ErrNoPasswordAccount):
			http.Error(w, "this account has no Misty password", http.StatusConflict)
		default:
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	}
}
