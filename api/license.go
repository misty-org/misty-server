package api

import (
	"encoding/json"
	"net/http"

	"github.com/kannachi323/misty/server/core/license"
	"github.com/kannachi323/misty/server/db"
	"golang.org/x/crypto/bcrypt"
)

// ValidateLicense is called by the local proxy to perform the one-time
// entitlement verification for perpetual pro access.
func ValidateLicense(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		user, hash, err := database.GetUserByEmail(body.Email)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if user == nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil {
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}

		sub, err := database.GetSubscription(user.ID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		if sub.Status != "active" {
			http.Error(w, "subscription inactive", http.StatusForbidden)
			return
		}

		token, err := license.Issue(user, sub)
		if err != nil {
			http.Error(w, "failed to issue license", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"license_token": token,
			"tier":          string(sub.Tier),
		})
	}
}
