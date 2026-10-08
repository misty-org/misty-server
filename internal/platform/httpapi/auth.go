package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kannachi323/misty/server/internal/accounts"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
	"github.com/kannachi323/misty/server/internal/platform/telemetry"
	"golang.org/x/crypto/bcrypt"
)

func RegisterWithTelemetry(database *db.Database, analytics telemetry.Client) http.HandlerFunc {
	return RegisterWithVerification(database, analytics, nil)
}

// RegisterWithVerification also sends the new account's email verification.
func RegisterWithVerification(database *db.Database, analytics telemetry.Client, verification *AccountVerification) http.HandlerFunc {
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
		if err := accounts.ValidateNewPassword(body.Password); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
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
			if errors.Is(err, db.ErrEmailTaken) {
				http.Error(w, "email already registered", http.StatusConflict)
				return
			}
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

		verification.AfterSignup(r, user, false)
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
		if blocked, retryAfter := loginFailures.Blocked(body.Email, time.Now()); blocked {
			w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
			http.Error(w, "too many sign-in attempts; try again later", http.StatusTooManyRequests)
			return
		}

		user, hash, err := database.GetUserByEmail(body.Email)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if user == nil || user.Provider != "misty" {
			// Spend the same bcrypt work as a real check, so response time does
			// not reveal which email addresses have password accounts.
			_ = bcrypt.CompareHashAndPassword(unknownAccountHash(), []byte(body.Password))
			loginFailures.Fail(body.Email, time.Now())
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil {
			loginFailures.Fail(body.Email, time.Now())
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		loginFailures.Succeed(body.Email)

		writeAuthSession(w, r, database, user, http.StatusOK)
	}
}

// unknownAccountHash is a bcrypt hash of a random value at the default cost,
// compared against when no password account matches the email.
var unknownAccountHash = sync.OnceValue(func() []byte {
	secret, _ := security.GenerateSecureToken()
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return []byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinval")
	}
	return hash
})

func writeAuthSession(
	w http.ResponseWriter,
	r *http.Request,
	database *db.Database,
	user *db.User,
	status int,
) {
	if err := issueSessionCookies(w, r, database, user.ID, db.SessionTTL); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, status, map[string]string{
		"user_id":  user.ID,
		"name":     user.Name,
		"username": user.Username,
		"email":    user.Email,
		"provider": user.Provider,
	})
}

// writeSessionCookie is the single definition of the session cookie shape. The
// cookie carries no Domain attribute on purpose. Browser clients send
// credentialed requests directly to the API origin, so the session should
// never be exposed to sibling subdomains.
func writeSessionCookie(w http.ResponseWriter, r *http.Request, token string, ttl time.Duration) {
	writeAuthCookie(w, r, TestingSessionCookieName, token, ttl)
}

func Logout(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		signer, err := security.SessionSignerFromEnv()
		if err != nil {
			http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
			return
		}
		for _, item := range []struct{ name, kind string }{{RefreshCookieName, "refresh"}, {TestingSessionCookieName, "access"}} {
			cookie, err := r.Cookie(item.name)
			if err != nil {
				continue
			}
			claims, err := signer.Verify(cookie.Value, item.kind)
			if err != nil {
				continue
			}
			if err := database.DeleteSession(security.HashToken(claims.SessionID)); err != nil {
				http.Error(w, "could not revoke session", http.StatusServiceUnavailable)
				return
			}
		}
		clearAuthCookies(w, r)

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
