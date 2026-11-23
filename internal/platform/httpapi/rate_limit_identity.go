package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/misty-org/misty-server/internal/accounts"
)

const identityKeyPrefixLength = 24

func RateLimitIdentity(r *http.Request) string {
	if credential := requestCredential(r); credential != "" {
		digest := sha256.Sum256([]byte(credential))
		return "acct:" + hex.EncodeToString(digest[:])[:identityKeyPrefixLength]
	}
	return "ip:" + ClientIPFromRequest(r)
}

func AuthenticatedRateLimitIdentity(r *http.Request) string {
	if userID, err := accounts.SessionUserID(r); err == nil && userID != "" {
		return "user:" + userID
	}
	return "ip:" + ClientIPFromRequest(r)
}

func requestCredential(r *http.Request) string {
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if authorization != "" {
		if parts := strings.Fields(authorization); len(parts) == 2 {
			return parts[1]
		}
		return authorization
	}
	if cookie, err := r.Cookie(SessionCookieName); err == nil && strings.TrimSpace(cookie.Value) != "" {
		return cookie.Value
	}
	return ""
}

func TestingIdentityIsAccount(key string) bool {
	return strings.HasPrefix(key, "acct:")
}
