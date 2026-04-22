package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/kannachi323/misty/server/db"
	"github.com/kannachi323/misty/server/security"
	"golang.org/x/crypto/bcrypt"
)

func Register(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name     string `json:"name"`
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" || body.Password == "" {
			http.Error(w, "email and password required", http.StatusBadRequest)
			return
		}
		body.Email = strings.TrimSpace(body.Email)
		if body.Email == "" {
			http.Error(w, "email and password required", http.StatusBadRequest)
			return
		}

		existing, _, err := database.GetUserByEmail(body.Email)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if existing != nil {
			http.Error(w, "email already registered", http.StatusConflict)
			return
		}

		user, err := database.CreateUser(body.Name, body.Email, body.Password)
		if err != nil {
			http.Error(w, "failed to create user", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"user_id": user.ID})
	}
}

func Login(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		body.Email = strings.TrimSpace(body.Email)
		if body.Email == "" || body.Password == "" {
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

		token, err := security.GenerateSecureToken()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		tokenHash := security.HashToken(token)
		if err := database.CreateSession(tokenHash, user.ID); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(db.SessionTTL.Seconds()),
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"user_id": user.ID,
			"name":    user.Name,
			"email":   user.Email,
		})
	}
}

func Logout(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err == nil {
			tokenHash := security.HashToken(cookie.Value)
			_ = database.DeleteSession(tokenHash)
		}

		secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
