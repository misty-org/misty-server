package api

import (
	"github.com/kannachi323/misty/server/internal/accounts"
	"net/http"

	"github.com/kannachi323/misty/server/internal/platform/db"
)

const SessionCookieName = "misty_session"

func sessionUserID(r *http.Request, _ *db.Database) (string, error) { return accounts.SessionUserID(r) }

func BearerTokenFromRequest(r *http.Request) (string, bool) {
	return accounts.BearerTokenFromRequest(r)
}

func GetMe(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := sessionUserID(r, database)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if userID == "" {
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}

		user, err := database.GetUserByID(userID)
		if err != nil || user == nil {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}

		verifiedAt, verifyErr := database.EmailVerifiedAt(r.Context(), userID)

		writeJSON(w, http.StatusOK, map[string]any{
			"id":                 user.ID,
			"username":           user.Username,
			"email":              user.Email,
			"provider":           user.Provider,
			"credential_account": user.CredentialPassword,
			"avatar_version":     user.AvatarVersion,
			"created_at":         user.CreatedAt,
			"allows_use":         true,
			"license_device":     "",
			"email_verified":     verifyErr == nil && verifiedAt != nil,
		})
	}
}
