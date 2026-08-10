package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// Rate-limit identity.
//
// Keying purely on IP leaves a hole: an authenticated caller spread across many
// addresses — a botnet, a rotating proxy pool, or just a phone moving between
// networks — gets a fresh budget per address. For endpoints that cost money
// that is the whole ballgame, so those are keyed on the *account* instead and
// fall back to the address only for unauthenticated traffic.
//
// The credential is never stored or logged: it is hashed, and only a prefix of
// the digest is kept, which is enough to partition callers without turning the
// limiter's state into a table of live session tokens.

const identityKeyPrefixLength = 24

// rateLimitIdentity returns the key a request is charged against.
func TestingRateLimitIdentity(r *http.Request) string {
	if credential := requestCredential(r); credential != "" {
		digest := sha256.Sum256([]byte(credential))
		return "acct:" + hex.EncodeToString(digest[:])[:identityKeyPrefixLength]
	}
	return "ip:" + TestingClientIPFromRequest(r)
}

// requestCredential extracts whatever identifies the caller's account, without
// a database lookup — this runs on every request, including ones that are about
// to be rejected.
func requestCredential(r *http.Request) string {
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if authorization != "" {
		// Both "Bearer <token>" and a bare token are accepted, matching how the
		// handlers read it.
		if parts := strings.Fields(authorization); len(parts) == 2 {
			return parts[1]
		}
		return authorization
	}
	// Session cookies are the other way a browser client authenticates.
	if cookie, err := r.Cookie(TestingSessionCookieName); err == nil && strings.TrimSpace(cookie.Value) != "" {
		return cookie.Value
	}
	return ""
}

// identityIsAccount reports whether a key came from a credential rather than an
// address, so callers can apply account-scoped policy only where it is real.
func TestingIdentityIsAccount(key string) bool {
	return strings.HasPrefix(key, "acct:")
}
