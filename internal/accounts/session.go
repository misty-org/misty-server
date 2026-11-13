package accounts

import (
	"github.com/misty-org/misty-server/internal/platform/security"
	"github.com/misty-org/misty-server/internal/platform/transport"
	"net/http"
	"strings"
)

const SessionCookieName = "misty_session"

// accessToken is the access JWT a request presents: the desktop app's bearer, or the website's
// cookie. A bearer always speaks for the request, so one that fails to verify is never rescued by
// a cookie sent with it.
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

// SessionUserID is the account a request is signed in as, or "" when it is not. Other bearer
// tokens (a run's MCP token, the operator's) don't verify as access tokens, so they are not signed in.
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

// SessionID returns the account session that signed this request's access
// token, so a long-lived stream can revalidate that session later. It is empty
// when the request has no valid access token.
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
