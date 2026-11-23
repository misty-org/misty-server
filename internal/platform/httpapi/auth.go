package api

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/misty-org/misty-server/internal/accounts"
	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
	"golang.org/x/crypto/bcrypt"
)

func Register(database *db.Database) http.HandlerFunc {
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
		if err := checkNewPasswordBreached(r.Context(), body.Password); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		existing, _, err := database.GetUserByEmail(body.Email)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if existing != nil && existing.Provider != "misty" {
			writeUseGoogle(w)
			return
		}
		if existing != nil {
			http.Error(w, "email already registered", http.StatusConflict)
			return
		}
		if db.IsServerOwnerEmail(body.Email) {
			writeOwnerUsesGoogle(w)
			return
		}
		if allowed, err := registrationAllowed(r.Context(), database, body.Email); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		} else if !allowed {
			writeRegistrationClosed(w)
			return
		}
		anyAccount, err := database.AnyAccount(r.Context())
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if anyAccount && confirmsEmail() {
			holdForConfirmation(w, r, database, db.PendingAccount{Username: body.Username, Email: body.Email, Password: body.Password})
			return
		}
		hold := anyAccount && openSignup()

		user, err := database.CreateUserWithUsername(body.Username, body.Email, body.Password)
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
		if hold {
			if err := openSignupLimits(r.Context(), database, user.ID); err != nil {
				http.Error(w, "failed to create user", http.StatusInternalServerError)
				return
			}
		}

		writeAuthSession(w, r, database, user, http.StatusCreated)
	}
}

func writeOwnerUsesGoogle(w http.ResponseWriter) {
	writeJSON(w, http.StatusConflict, map[string]string{"code": "use_google", "message": "This server's owner signs in with Google. Please use Continue with Google instead."})
}

func writeUseGoogle(w http.ResponseWriter) {
	writeJSON(w, http.StatusConflict, map[string]string{"code": "use_google", "message": "This email already exists as a Misty account. Please use Continue with Google instead."})
}

func Login(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
			Waiting  bool   `json:"waiting"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		body.Email = strings.TrimSpace(body.Email)
		if body.Email == "" || body.Password == "" {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		failures := passwordFailureCaps(r, body.Email)
		if lockedOut(w, r, database, failures, "too many sign-in attempts; try again later") {
			return
		}

		user, hash, err := database.GetUserByEmail(body.Email)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if user == nil && pendingSignIn(w, r, database, body.Email, body.Password, body.Waiting) {
			return
		}
		if user != nil && user.Provider != "misty" {
			writeUseGoogle(w)
			return
		}
		if user == nil {
			recordFailure(r, database, failures)
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil {
			recordFailure(r, database, failures)
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		clearFailures(r, database, failures)
		database.UpgradePasswordHash(user.ID, hash, body.Password)
		if allowed, err := signInAllowed(r.Context(), database, user); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		} else if !allowed {
			writeEmailNotAllowed(w)
			return
		}

		if body.Waiting {
			writeJSON(w, http.StatusOK, map[string]string{"status": "confirmed"})
			return
		}
		writeAuthSession(w, r, database, user, http.StatusOK)
	}
}

var unknownAccountHash = sync.OnceValue(func() []byte {
	secret, _ := security.GenerateSecureToken()
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), accounts.PasswordCost)
	if err != nil {
		return []byte("$2a$12$invalidinvalidinvalidinvalidinvalidinvalidinvalidinval")
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
	writeAuthSessionWith(w, r, database, user, status, nil)
}

func writeAuthSessionWith(
	w http.ResponseWriter,
	r *http.Request,
	database *db.Database,
	user *db.User,
	status int,
	extra map[string]any,
) {
	if suspended, err := database.AccountSuspended(r.Context(), user.ID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	} else if suspended {
		writeSuspendedSignIn(w, user.ID)
		return
	}
	if err := issueSession(w, r, database, user.ID, db.SessionTTL); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	body := map[string]any{
		"user_id":  user.ID,
		"username": user.Username,
		"email":    user.Email,
		"provider": user.Provider,
	}
	for key, value := range extra {
		body[key] = value
	}
	writeJSON(w, status, body)
}

func writeSessionCookie(w http.ResponseWriter, r *http.Request, token string, ttl time.Duration) {
	writeAuthCookie(w, r, SessionCookieName, token, ttl)
}

func Logout(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		signer, err := security.SessionSignerFromEnv()
		if err != nil {
			http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
			return
		}
		for _, presented := range presentedSessionTokens(r) {
			claims, err := signer.Verify(presented.token, presented.kind)
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

type sessionToken struct{ token, kind string }

func presentedSessionTokens(r *http.Request) []sessionToken {
	var tokens []sessionToken
	if token, ok := BearerTokenFromRequest(r); ok {
		tokens = append(tokens, sessionToken{token, "refresh"}, sessionToken{token, "access"})
	}
	if cookie, err := r.Cookie(RefreshCookieName); err == nil {
		tokens = append(tokens, sessionToken{cookie.Value, "refresh"})
	}
	if cookie, err := r.Cookie(SessionCookieName); err == nil {
		tokens = append(tokens, sessionToken{cookie.Value, "access"})
	}
	return tokens
}

func IsSecureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func SessionCookieSameSite(r *http.Request, secure bool) http.SameSite {
	if secure && r.Header.Get("Origin") != "" {
		return http.SameSiteNoneMode
	}
	return http.SameSiteLaxMode
}
