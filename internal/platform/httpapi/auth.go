package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"github.com/kannachi323/misty/server/internal/platform/telemetry"
	"golang.org/x/crypto/bcrypt"
)

func Register(database *db.Database) http.HandlerFunc {
	return RegisterWithTelemetry(database, telemetry.NoopClient{})
}

func RegisterWithTelemetry(database *db.Database, analytics telemetry.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name     string `json:"name"`
			Username string `json:"username"`
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		if body.Username == "" || body.Email == "" || body.Password == "" {
			http.Error(w, "username, email, and password required", http.StatusBadRequest)
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

		user, err := database.CreateUserWithUsername(body.Name, body.Username, body.Email, body.Password)
		if err != nil {
			if errors.Is(err, db.ErrInvalidUsername) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if errors.Is(err, db.ErrUsernameTaken) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			http.Error(w, "failed to create user", http.StatusInternalServerError)
			return
		}
		if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Misty-Analytics-Enabled")), "true") {
			if err := database.UpdateTelemetryPreferences(user.ID, true, false); err == nil {
				analytics.UserRegistered(user.ID, r.Header.Get("X-Misty-Platform"), r.Header.Get("X-Misty-Release-Channel"))
			}
		}

		writeAuthSession(w, r, database, user, http.StatusCreated)
	}
}

func Login(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if decodeJSON(w, r, &body) != nil {
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
		if !acceptSelfHostLoginProof(w, r, database, user.ID) {
			return
		}

		writeAuthSession(w, r, database, user, http.StatusOK)
	}
}

func writeAuthSession(
	w http.ResponseWriter,
	r *http.Request,
	database *db.Database,
	user *db.User,
	status int,
) {
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

	writeSessionCookie(w, r, token, db.SessionTTL)

	writeJSON(w, status, map[string]string{
		"user_id":  user.ID,
		"name":     user.Name,
		"username": user.Username,
		"email":    user.Email,
		"token":    token,
	})
}

// writeSessionCookie is the single definition of the session cookie shape. The
// cookie carries no Domain attribute on purpose. Browser clients send
// credentialed requests directly to the API origin, so the session should
// never be exposed to sibling subdomains.
func writeSessionCookie(w http.ResponseWriter, r *http.Request, token string, ttl time.Duration) {
	secure := TestingIsSecureRequest(r)
	http.SetCookie(w, &http.Cookie{
		Name:     TestingSessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: TestingSessionCookieSameSite(r, secure),
		MaxAge:   int(ttl.Seconds()),
	})
}

func Logout(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token, ok := sessionTokenFromRequest(r); ok {
			tokenHash := security.HashToken(token)
			_ = database.DeleteSession(tokenHash)
		}

		secure := TestingIsSecureRequest(r)
		http.SetCookie(w, &http.Cookie{
			Name:     TestingSessionCookieName,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   secure,
			SameSite: TestingSessionCookieSameSite(r, secure),
			MaxAge:   -1,
		})

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func TestingIsSecureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func TestingSessionCookieSameSite(r *http.Request, secure bool) http.SameSite {
	if secure && r.Header.Get("Origin") != "" {
		return http.SameSiteNoneMode
	}
	return http.SameSiteLaxMode
}
