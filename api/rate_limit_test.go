package api

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestForgotPasswordRateLimiter(t *testing.T) {
	limiter := NewForgotPasswordRateLimiter(2, time.Minute)
	now := time.Now()

	if !limiter.Allow("127.0.0.1|user@example.com", now) {
		t.Fatal("first request should be allowed")
	}
	if !limiter.Allow("127.0.0.1|user@example.com", now.Add(10*time.Second)) {
		t.Fatal("second request should be allowed")
	}
	if limiter.Allow("127.0.0.1|user@example.com", now.Add(20*time.Second)) {
		t.Fatal("third request should be rate-limited")
	}
	if !limiter.Allow("127.0.0.1|user@example.com", now.Add(2*time.Minute)) {
		t.Fatal("request after window should be allowed")
	}
}

func TestForgotPasswordRateLimitKeyUsesForwardedIP(t *testing.T) {
	req := httptest.NewRequest("POST", "/auth/forgot", nil)
	req.RemoteAddr = "10.0.0.8:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.8")

	key := forgotPasswordRateLimitKey(req, "User@example.com")
	if key != "203.0.113.5|user@example.com" {
		t.Fatalf("forgotPasswordRateLimitKey() = %q", key)
	}
}
