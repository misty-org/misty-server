package accounts

import (
	"github.com/kannachi323/misty/server/internal/platform/security"
	"github.com/kannachi323/misty/server/internal/platform/transport"
	"net/http"
	"strings"
)

const SessionCookieName = "misty_session"

func SessionUserID(r *http.Request) (string, error) {
	_, bearer := BearerTokenFromRequest(r)
	if !bearer {
		cookie, err := r.Cookie(SessionCookieName)
		if err != nil {
			return "", nil
		}
		signer, err := security.SessionSignerFromEnv()
		if err != nil {
			return "", err
		}
		claims, err := signer.Verify(cookie.Value, "access")
		if err != nil {
			return "", nil
		}
		return claims.Subject, nil
	}
	// Installed-app bearer sessions are retired. Only the signed account cookie authenticates browser requests.
	return "", nil
}

func BearerTokenFromRequest(r *http.Request) (string, bool) {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	scheme, token, ok := strings.Cut(authHeader, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

func AuthenticatedUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	user, err := SessionUserID(r)
	if err != nil {
		transport.WriteJSON(w, 500, map[string]string{"code": "internal_error"})
		return "", false
	}
	if user == "" {
		transport.WriteJSON(w, 401, map[string]string{"code": "not_authenticated"})
		return "", false
	}
	return user, true
}
