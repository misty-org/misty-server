package accounts

import (
	"github.com/misty-org/misty-server/internal/platform/security"
	"github.com/misty-org/misty-server/internal/platform/transport"
	"net/http"
	"strings"
)

const SessionCookieName = "misty_session"

func accessToken(r *http.Request) (string, bool) {
	if token, bearer := BearerTokenFromRequest(r); bearer {
		return token, true
	}
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return "", false
	}
	return cookie.Value, true
}

func SessionUserID(r *http.Request) (string, error) {
	token, ok := accessToken(r)
	if !ok {
		return "", nil
	}
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		return "", err
	}
	claims, err := signer.Verify(token, "access")
	if err != nil {
		return "", nil
	}
	return claims.Subject, nil
}

func SessionID(r *http.Request) string {
	token, ok := accessToken(r)
	if !ok {
		return ""
	}
	signer, err := security.SessionSignerFromEnv()
	if err != nil {
		return ""
	}
	claims, err := signer.Verify(token, "access")
	if err != nil {
		return ""
	}
	return claims.SessionID
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
