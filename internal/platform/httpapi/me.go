package api

import (
	"errors"
	"github.com/misty-org/misty-server/internal/accounts"
	"net/http"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
)

const SessionCookieName = "misty_session"

func sessionUserID(r *http.Request, _ *db.Database) (string, error) { return accounts.SessionUserID(r) }

func BearerTokenFromRequest(r *http.Request) (string, bool) {
	return accounts.BearerTokenFromRequest(r)
}

func UpdateUsername(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, database)
		if !ok {
			return
		}
		var body struct {
			Username string `json:"username"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		username, err := database.SetUsername(userID, body.Username)
		switch {
		case errors.Is(err, db.ErrInvalidUsername):
			http.Error(w, err.Error(), http.StatusBadRequest)
		case errors.Is(err, db.ErrUsernameTaken):
			http.Error(w, err.Error(), http.StatusConflict)
		case err != nil:
			http.Error(w, "internal error", http.StatusInternalServerError)
		default:
			writeJSON(w, http.StatusOK, map[string]string{"username": username})
		}
	}
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

		var deleteAt any
		if at, closing := suspensions.Closing(r.Context(), userID); closing {
			deleteAt = at.UTC().Format(time.RFC3339)
		}

		owner, _ := database.IsServerOwner(r.Context(), userID)

		limits, _ := database.AccountLimitsFor(r.Context(), userID)
		devicesAllowed := limits.Devices == nil || *limits.Devices > 0

		levels, _ := db.SpaceLevelsConfigured()

		writeJSON(w, http.StatusOK, map[string]any{
			"id":              user.ID,
			"username":        user.Username,
			"email":           user.Email,
			"provider":        user.Provider,
			"avatar_version":  user.AvatarVersion,
			"banner_version":  user.BannerVersion,
			"created_at":      user.CreatedAt,
			"delete_at":       deleteAt,
			"server_owner":    owner,
			"devices_allowed": devicesAllowed,
			"space_levels":    levels != nil,
		})
	}
}
