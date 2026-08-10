package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestValidateResetURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		ok   bool
	}{
		{name: "https", raw: "https://app.example.com/reset", ok: true},
		{name: "localhost", raw: "http://localhost:5173/reset", ok: true},
		{name: "missing_host", raw: "/reset", ok: false},
		{name: "http_non_localhost", raw: "http://example.com/reset", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := TestingValidateResetURL(tt.raw)
			if (err == nil) != tt.ok {
				t.Fatalf("validateResetURL(%q) error = %v, want ok=%v", tt.raw, err, tt.ok)
			}
		})
	}
}

func TestBuildAndClearPasswordResetCookie(t *testing.T) {
	expiresAt := time.Now().Add(time.Minute)
	cookie := TestingBuildPasswordResetCookie("token-123", expiresAt, true)

	if cookie.Name != TestingPasswordResetCookieName {
		t.Fatalf("cookie.Name = %q, want %q", cookie.Name, TestingPasswordResetCookieName)
	}
	if cookie.Value != "token-123" {
		t.Fatalf("cookie.Value = %q, want %q", cookie.Value, "token-123")
	}
	if !cookie.HttpOnly || !cookie.Secure {
		t.Fatalf("cookie flags = httpOnly:%v secure:%v, want both true", cookie.HttpOnly, cookie.Secure)
	}

	rec := httptest.NewRecorder()
	TestingClearPasswordResetCookie(rec)
	resp := rec.Result()
	defer resp.Body.Close()

	found := false
	for _, cleared := range resp.Cookies() {
		if cleared.Name == TestingPasswordResetCookieName {
			found = true
			if cleared.MaxAge != -1 {
				t.Fatalf("cleared cookie MaxAge = %d, want -1", cleared.MaxAge)
			}
		}
	}
	if !found {
		t.Fatalf("cleared %q cookie not found", TestingPasswordResetCookieName)
	}
}

func TestReadResetTokenCookie(t *testing.T) {
	service := &PasswordResetService{}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: TestingPasswordResetCookieName, Value: " token "})
	token, err := service.TestingReadResetTokenCookie(req)
	if err != nil {
		t.Fatalf("readResetTokenCookie() error = %v", err)
	}
	if token != "token" {
		t.Fatalf("token = %q, want %q", token, "token")
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: TestingPasswordResetCookieName, Value: "   "})
	if _, err := service.TestingReadResetTokenCookie(req); err == nil {
		t.Fatal("readResetTokenCookie() succeeded with empty token, want error")
	}
}
