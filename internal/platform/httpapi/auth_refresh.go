package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
	"github.com/misty-org/misty-server/internal/platform/security"
)

const RefreshCookieName = "misty_refresh"

// The desktop app holds its JWTs in native code, so it asks for them in response headers
// instead of cookies. The website only ever gets cookies.
const (
	TokenTransportHeader = "X-Misty-Token-Transport"
	AccessTokenHeader    = "X-Misty-Access-Token"
	RefreshTokenHeader   = "X-Misty-Refresh-Token"
	SessionEndedHeader   = "X-Misty-Session-Ended"
)

// usesTokenHeaders says whether this request may receive tokens it can read. Browsers send
// Origin on every POST, and only POST issues a session here, so a page or an injected script
// can never reach this: whatever it sets, its answer stays in HttpOnly cookies.
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
	if err := database.CreateRefreshSession(r.Context(), security.HashToken(sid), security.HashToken(refresh), userID, expires); err != nil {
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

// clearAuthCookies ends the session on the client: the desktop app is told in a header, and
// cookies are expired. A request that asks for headers gets Set-Cookie only if it came with cookies.
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

// presentedRefreshToken is the refresh JWT a request presents, and whether it came as a bearer.
// A bearer speaks for the request: one that fails is never rescued by a cookie sent with it.
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
		// Taking someone off the allowlist ends their session here, not only their next sign-in.
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
		// Tokens leave in headers only for a refresh token that arrived readable: one held in a
		// cookie stays out of reach of whoever sent this request, whatever else it asked for.
		if viaBearer && usesTokenHeaders(r) {
			writeTokenHeaders(w, access, refresh)
		} else {
			writeJWTCookies(w, r, access, refresh, claims.ExpiresAt.Time)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// Require an explicit non-simple header on cookie-authenticated mutations.
// CORS is not itself CSRF protection: reject disallowed origins before handlers.
// A bearer request sends no cookie, so no other site can make a browser send it for
// someone; it passes unless it also carries a cookie or is a sign-in.
func CookieCSRFProtection(allowedOrigin func(string) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			_, accessErr := r.Cookie(SessionCookieName)
			_, refreshErr := r.Cookie(RefreshCookieName)
			// Login also needs protection against login CSRF.
			path := r.URL.Path
			for _, prefix := range []string{"/api/v1", "/v1", "/api"} {
				if strings.HasPrefix(path, prefix+"/") {
					path = strings.TrimPrefix(path, prefix)
					break
				}
			}
			// A confirmation link's form comes from the website, with any cookies this browser
			// has here. Its token is all it uses, and it opens no session.
			if path == "/auth/confirm-email" {
				next.ServeHTTP(w, r)
				return
			}
			login := path == "/login" || path == "/api/login" || path == "/register" || path == "/api/register" ||
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
