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
	registerToken, ok := registerBody["token"].(string)
	if !ok || registerToken == "" {
		t.Fatalf("register response missing token: %#v", registerBody)
	}

	registerMeRec := performBearerJSONRequest(t, api.GetMe(database), http.MethodGet, "/me", nil, registerToken)
	if registerMeRec.Code != http.StatusOK {
		t.Fatalf("register bearer /me status = %d, want %d, body = %q", registerMeRec.Code, http.StatusOK, registerMeRec.Body.String())
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
	sessionToken, ok := loginBody["token"].(string)
	if !ok || sessionToken == "" {
		t.Fatalf("login response missing token: %#v", loginBody)
	}

	meRec := performJSONRequest(t, api.GetMe(database), http.MethodGet, "/me", nil, sessionCookie)
	if meRec.Code != http.StatusOK {
		t.Fatalf("/me status = %d, want %d, body = %q", meRec.Code, http.StatusOK, meRec.Body.String())
	}

	bearerMeRec := performBearerJSONRequest(t, api.GetMe(database), http.MethodGet, "/me", nil, sessionToken)
	if bearerMeRec.Code != http.StatusOK {
		t.Fatalf("bearer /me status = %d, want %d, body = %q", bearerMeRec.Code, http.StatusOK, bearerMeRec.Body.String())
	}

	logoutRec := performBearerJSONRequest(t, api.Logout(database), http.MethodPost, "/logout", nil, sessionToken)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want %d", logoutRec.Code, http.StatusOK)
	}

	meAfterLogout := performJSONRequest(t, api.GetMe(database), http.MethodGet, "/me", nil, sessionCookie)
	if meAfterLogout.Code != http.StatusUnauthorized {
		t.Fatalf("/me after logout status = %d, want %d", meAfterLogout.Code, http.StatusUnauthorized)
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
