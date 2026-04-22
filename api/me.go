package api

import (
	"net/http"
	"strings"

	"github.com/kannachi323/misty/server/db"
	"github.com/kannachi323/misty/server/security"
)

const sessionCookieName = "misty_session"

func sessionUserID(r *http.Request, database *db.Database) (string, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", nil
	}
	tokenHash := security.HashToken(cookie.Value)
	return database.GetSessionUserID(tokenHash)
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

		sub, err := database.GetSubscription(userID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"id":             user.ID,
			"name":           user.Name,
			"email":          user.Email,
			"created_at":     user.CreatedAt,
			"tier":           string(sub.Tier),
			"status":         sub.Status,
			"expires_at":     sub.ExpiresAt,
			"license_device": sub.LicenseDevice,
		})
	}
}

func UpdateProfile(database *db.Database) http.HandlerFunc {
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

		var body struct {
			Name string `json:"name"`
		}
		if err := decodeJSON(w, r, &body); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		body.Name = strings.TrimSpace(body.Name)
		if body.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}

		if err := database.UpdateUserName(userID, body.Name); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func UpdateDevice(database *db.Database) http.HandlerFunc {
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

		var body struct {
			Device string `json:"device"`
		}
		if err := decodeJSON(w, r, &body); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		if err := database.UpdateLicenseDevice(userID, body.Device); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
