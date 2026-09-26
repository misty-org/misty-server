package integration

import (
	"net/http"
	"testing"

	api "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestAuthLifecycle(t *testing.T) {
	database := openIntegrationDatabase(t)

	registerRec := performJSONRequest(t, api.Register(database), http.MethodPost, "/register", map[string]string{
		"name":     "Ada Lovelace",
		"username": "ada_lovelace",
		"email":    "ada@example.com",
		"password": "correct horse battery staple",
	})
	if registerRec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d, body = %q", registerRec.Code, http.StatusCreated, registerRec.Body.String())
	}
	registerCookie := requireCookie(t, registerRec, sessionCookieName)
	if !registerCookie.HttpOnly {
		t.Fatal("register session cookie should be HttpOnly")
	}
	registerBody := decodeJSONResponse(t, registerRec)
	if registerBody["username"] != "ada_lovelace" {
		t.Fatalf("register username = %#v, want ada_lovelace", registerBody["username"])
	}
	if _, ok := registerBody["token"]; ok {
		t.Fatal("login credentials must not be returned in JSON")
	}
	registerMeRec := performJSONRequest(t, api.GetMe(database), http.MethodGet, "/me", nil, registerCookie)
	if registerMeRec.Code != http.StatusOK {
		t.Fatalf("register /me=%d: %s", registerMeRec.Code, registerMeRec.Body.String())
	}
	registerMeBody := decodeJSONResponse(t, registerMeRec)
	if registerMeBody["username"] != "ada_lovelace" {
		t.Fatalf("register /me username = %#v, want ada_lovelace", registerMeBody["username"])
	}
	duplicateUsernameRec := performJSONRequest(t, api.Register(database), http.MethodPost, "/register", map[string]string{
		"name":     "Another Ada",
		"username": "ADA_LOVELACE",
		"email":    "another-ada@example.com",
		"password": "correct horse battery staple",
	})
	if duplicateUsernameRec.Code != http.StatusConflict {
		t.Fatalf("duplicate username status = %d, want %d, body = %q", duplicateUsernameRec.Code, http.StatusConflict, duplicateUsernameRec.Body.String())
	}

	loginRec := performJSONRequest(t, api.Login(database), http.MethodPost, "/login", map[string]string{
		"email":    "ada@example.com",
		"password": "correct horse battery staple",
	})
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d, body = %q", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}

	sessionCookie := requireCookie(t, loginRec, sessionCookieName)
	if !sessionCookie.HttpOnly {
		t.Fatal("session cookie should be HttpOnly")
	}
	loginBody := decodeJSONResponse(t, loginRec)
	if _, ok := loginBody["token"]; ok {
		t.Fatal("login credentials must not be returned in JSON")
	}
	refreshCookie := requireCookie(t, loginRec, api.RefreshCookieName)
	if !refreshCookie.HttpOnly {
		t.Fatal("refresh cookie must be HttpOnly")
	}

	meRec := performJSONRequest(t, api.GetMe(database), http.MethodGet, "/me", nil, sessionCookie)
	if meRec.Code != http.StatusOK {
		t.Fatalf("/me status = %d, want %d, body = %q", meRec.Code, http.StatusOK, meRec.Body.String())
	}

	bearerMeRec := performBearerJSONRequest(t, api.GetMe(database), http.MethodGet, "/me", nil, sessionCookie.Value)
	if bearerMeRec.Code != http.StatusUnauthorized {
		t.Fatalf("account JWT accepted as bearer: %d", bearerMeRec.Code)
	}
	refreshRec := performJSONRequest(t, api.RefreshSession(database), http.MethodPost, "/auth/refresh", nil, refreshCookie)
	if refreshRec.Code != http.StatusNoContent {
		t.Fatalf("refresh=%d: %s", refreshRec.Code, refreshRec.Body.String())
	}
	nextRefresh := requireCookie(t, refreshRec, api.RefreshCookieName)
	if nextRefresh.Value == refreshCookie.Value {
		t.Fatal("refresh cookie was not rotated")
	}
	logoutRec := performJSONRequest(t, api.Logout(database), http.MethodPost, "/logout", nil, nextRefresh)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout=%d", logoutRec.Code)
	}
	for _, cookie := range logoutRec.Result().Cookies() {
		if cookie.MaxAge != -1 {
			t.Fatal("logout must expire both cookies")
		}
	}
	replayRec := performJSONRequest(t, api.RefreshSession(database), http.MethodPost, "/auth/refresh", nil, nextRefresh)
	if replayRec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh after logout=%d", replayRec.Code)
	}
	// Stateless access credentials remain valid until their five-minute expiry.
	meAfterLogout := performJSONRequest(t, api.GetMe(database), http.MethodGet, "/me", nil, sessionCookie)
	if meAfterLogout.Code != http.StatusOK {
		t.Fatalf("access token should remain valid until expiry: %d", meAfterLogout.Code)
	}

}

func TestAuthHandlersErrorPaths(t *testing.T) {
	database := openIntegrationDatabase(t)

	if rec := performJSONRequest(t, api.Register(database), http.MethodPost, "/register", map[string]string{"email": "   ", "password": "pw"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("register missing email status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if rec := performJSONRequest(t, api.Register(database), http.MethodPost, "/register", map[string]string{"username": "has-dash", "email": "valid@example.com", "password": "pw"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("register invalid username status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	if rec := performJSONRequest(t, api.Login(database), http.MethodPost, "/login", map[string]string{"email": "user@example.com"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("login missing password status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
