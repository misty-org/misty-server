package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
)

const RefreshCookieName = "misty_refresh"

const (
	TokenTransportHeader = "X-Misty-Token-Transport"
	AccessTokenHeader    = "X-Misty-Access-Token"
	RefreshTokenHeader   = "X-Misty-Refresh-Token"
	SessionEndedHeader   = "X-Misty-Session-Ended"
)

func usesTokenHeaders(r *http.Request) bool {
	_, hasOrigin := r.Header["Origin"]
	return r.Method == http.MethodPost && !hasOrigin &&
		strings.EqualFold(strings.TrimSpace(r.Header.Get(TokenTransportHeader)), "bearer")
}

func writeTokenHeaders(w http.ResponseWriter, access, refresh string) {
	w.Header().Set(AccessTokenHeader, access)
	w.Header().Set(RefreshTokenHeader, refresh)
	w.Header().Set("Cache-Control", "no-store")
}

func issueSession(w http.ResponseWriter, r *http.Request, database *db.Database, userID string, ttl time.Duration) error {
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
	if err := database.CreateNamedRefreshSession(r.Context(), security.HashToken(sid), security.HashToken(refresh), userID, sessionNameFor(r), expires); err != nil {
		return err
	}
	if usesTokenHeaders(r) {
		writeTokenHeaders(w, access, refresh)
	} else {
		writeJWTCookies(w, r, access, refresh, expires)
	}
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
	secure := IsSecureRequest(r)
	maxAge := int(ttl.Seconds())
	if ttl < 0 {
		maxAge = -1
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true,
		Secure: secure, SameSite: SessionCookieSameSite(r, secure), MaxAge: maxAge})
}

func clearAuthCookies(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if usesTokenHeaders(r) {
		w.Header().Set(SessionEndedHeader, "1")
		if !hasAuthCookie(r) {
			return
		}
	}
	writeAuthCookie(w, r, SessionCookieName, "", -time.Second)
	writeAuthCookie(w, r, RefreshCookieName, "", -time.Second)
}

func hasAuthCookie(r *http.Request) bool {
	_, accessErr := r.Cookie(SessionCookieName)
	_, refreshErr := r.Cookie(RefreshCookieName)
	return accessErr == nil || refreshErr == nil
}

func presentedRefreshToken(r *http.Request) (token string, bearer, ok bool) {
	if token, ok := BearerTokenFromRequest(r); ok {
		return token, true, true
	}
	cookie, err := r.Cookie(RefreshCookieName)
	if err != nil {
		return "", false, false
	}
	return cookie.Value, false, true
}

func RefreshSession(database *db.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		signer, err := security.SessionSignerFromEnv()
		if err != nil {
			http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
			return
		}
		presented, viaBearer, ok := presentedRefreshToken(r)
		if !ok {
			clearAuthCookies(w, r)
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		claims, err := signer.Verify(presented, "refresh")
		if err != nil {
			clearAuthCookies(w, r)
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		if suspended, err := database.AccountSuspended(r.Context(), claims.Subject); err != nil {
			http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
			return
		} else if suspended {
			clearAuthCookies(w, r)
			writeAccountSuspended(w)
			return
		}
		if allowedEmails() != nil {
			user, err := database.GetUserByID(claims.Subject)
			if err != nil {
				http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
				return
			}
			if user != nil {
				allowed, err := signInAllowed(r.Context(), database, user)
				if err != nil {
					http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
					return
				}
				if !allowed {
					clearAuthCookies(w, r)
					writeEmailNotAllowed(w)
					return
				}
			}
		}
		access, refresh, err := mintSessionPair(signer, claims.Subject, claims.SessionID, claims.ExpiresAt.Time)
		if err != nil {
			http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
			return
		}
		valid, err := database.RotateRefreshSession(r.Context(), security.HashToken(claims.SessionID), security.HashToken(presented), security.HashToken(refresh), claims.Subject)
		if err != nil {
			http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
			return
		}
		if !valid {
			clearAuthCookies(w, r)
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		if viaBearer && usesTokenHeaders(r) {
			writeTokenHeaders(w, access, refresh)
		} else {
			writeJWTCookies(w, r, access, refresh, claims.ExpiresAt.Time)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func CookieCSRFProtection(allowedOrigin func(string) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			_, accessErr := r.Cookie(SessionCookieName)
			_, refreshErr := r.Cookie(RefreshCookieName)
			path := strings.TrimPrefix(r.URL.Path, "/v1")
			if path == "/auth/confirm-email" {
				next.ServeHTTP(w, r)
				return
			}
			login := path == "/login" || path == "/register" ||
				path == "/auth/google" || path == "/auth/google/complete" || path == "/setup-keys/redeem" ||
				path == "/auth/confirm-email/sign-in"
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
