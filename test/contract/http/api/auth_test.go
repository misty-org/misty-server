package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestSessionCookieSameSiteUsesNoneForSecureAppOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	req.Header.Set("Origin", "tauri://localhost")

	if got := TestingSessionCookieSameSite(req, true); got != http.SameSiteNoneMode {
		t.Fatalf("sessionCookieSameSite() = %v, want %v", got, http.SameSiteNoneMode)
	}
}

func TestSessionCookieSameSiteKeepsLaxForInsecureRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	req.Header.Set("Origin", "http://localhost:5173")

	if got := TestingSessionCookieSameSite(req, false); got != http.SameSiteLaxMode {
		t.Fatalf("sessionCookieSameSite() = %v, want %v", got, http.SameSiteLaxMode)
	}
}

func TestBearerTokenFromRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer session-token")

	token, ok := TestingBearerTokenFromRequest(req)
	if !ok || token != "session-token" {
		t.Fatalf("bearerTokenFromRequest() = %q, %v; want session-token, true", token, ok)
	}
}

func TestBearerTokenFromRequestRejectsEmptyToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer   ")

	if token, ok := TestingBearerTokenFromRequest(req); ok || token != "" {
		t.Fatalf("bearerTokenFromRequest() = %q, %v; want empty, false", token, ok)
	}
}

func TestIsSecureRequestHonorsForwardedProtoCaseInsensitively(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	req.Header.Set("X-Forwarded-Proto", "HTTPS")

	if !TestingIsSecureRequest(req) {
		t.Fatal("isSecureRequest() = false, want true")
	}
}

func TestLoginRejectsUnknownJSONFieldsBeforeDatabaseAccess(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"email":"user@example.com","password":"pw","extra":true}`))
	rec := httptest.NewRecorder()

	Login(nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Login status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
