package api

import (
	"net/http"
	"strings"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	"github.com/kannachi323/misty/server/internal/platform/security"
)

const RefreshCookieName = "misty_refresh"

func issueSessionCookies(w http.ResponseWriter, r *http.Request, database *db.Database, userID string, ttl time.Duration) error {
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		return err
	}
	sid, err := security.GenerateSecureToken()
	if err != nil {
		return err
	}
	expires := time.Now().Add(ttl)
	access, refresh, err := mintSessionPair(signer, userID, sid, expires)
	if err != nil {
		return err
	}
	if err := database.CreateRefreshSession(r.Context(), security.HashToken(sid), security.HashToken(refresh), userID, expires); err != nil {
		return err
	}
	writeJWTCookies(w, r, access, refresh, expires)
	return nil
}

func mintSessionPair(signer *security.SessionSigner, userID, sid string, expires time.Time) (string, string, error) {
	accessExpiry := time.Now().Add(security.AccessTokenTTL)
	if expires.Before(accessExpiry) {
		accessExpiry = expires
	}
	access, err := signer.Mint(userID, sid, "access", accessExpiry)
	if err != nil {
		return "", "", err
	}
	refresh, err := signer.Mint(userID, sid, "refresh", expires)
	return access, refresh, err
}

func writeJWTCookies(w http.ResponseWriter, r *http.Request, access, refresh string, expires time.Time) {
	ttl := time.Until(expires)
	accessTTL := security.AccessTokenTTL
	if ttl < accessTTL {
		accessTTL = ttl
	}
	writeSessionCookie(w, r, access, accessTTL)
	writeAuthCookie(w, r, RefreshCookieName, refresh, ttl)
	w.Header().Set("Cache-Control", "no-store")
}

func writeAuthCookie(w http.ResponseWriter, r *http.Request, name, value string, ttl time.Duration) {
	secure := TestingIsSecureRequest(r)
	maxAge := int(ttl.Seconds())
	if ttl < 0 {
		maxAge = -1
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true,
		Secure: secure, SameSite: TestingSessionCookieSameSite(r, secure), MaxAge: maxAge})
}

func clearAuthCookies(w http.ResponseWriter, r *http.Request) {
	writeAuthCookie(w, r, TestingSessionCookieName, "", -time.Second)
	writeAuthCookie(w, r, RefreshCookieName, "", -time.Second)
	w.Header().Set("Cache-Control", "no-store")
}

func RefreshSession(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		signer, err := security.SessionSignerFromEnv()
		if err != nil {
			http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
			return
		}
		cookie, err := r.Cookie(RefreshCookieName)
		if err != nil {
			clearAuthCookies(w, r)
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		claims, err := signer.Verify(cookie.Value, "refresh")
		if err != nil {
			clearAuthCookies(w, r)
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		access, refresh, err := mintSessionPair(signer, claims.Subject, claims.SessionID, claims.ExpiresAt.Time)
		if err != nil {
			http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
			return
		}
		valid, err := database.RotateRefreshSession(r.Context(), security.HashToken(claims.SessionID), security.HashToken(cookie.Value), security.HashToken(refresh), claims.Subject)
		if err != nil {
			http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
			return
		}
		if !valid {
			clearAuthCookies(w, r)
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		writeJWTCookies(w, r, access, refresh, claims.ExpiresAt.Time)
		w.WriteHeader(http.StatusNoContent)
	}
}

// Require an explicit non-simple header on cookie-authenticated mutations.
// CORS is not itself CSRF protection: reject disallowed origins before handlers.
func CookieCSRFProtection(allowedOrigin func(string) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			_, accessErr := r.Cookie(TestingSessionCookieName)
			_, refreshErr := r.Cookie(RefreshCookieName)
			// Login and enrollment also need protection against login CSRF.
			path := r.URL.Path
			for _, prefix := range []string{"/api/v1", "/v1", "/api"} {
				if strings.HasPrefix(path, prefix+"/") {
					path = strings.TrimPrefix(path, prefix)
					break
				}
			}
			login := path == "/login" || path == "/api/login" || path == "/register" || path == "/api/register" ||
				path == "/self-host/bootstrap" || path == "/api/self-host/bootstrap" || path == "/self-host/enroll" || path == "/api/self-host/enroll"
			if accessErr == nil || refreshErr == nil || login {
				origin := r.Header.Get("Origin")
				if r.Header.Get("X-Misty-CSRF") != "1" || (origin != "" && !allowedOrigin(origin)) {
					http.Error(w, "request origin could not be verified", http.StatusForbidden)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
