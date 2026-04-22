package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestForgotPasswordRateLimiter(t *testing.T) {
	limiter := NewForgotPasswordRateLimiter(2, time.Minute)
	now := time.Now()

	if allowed, _ := limiter.Allow("127.0.0.1|user@example.com", now); !allowed {
		t.Fatal("first request should be allowed")
	}
	if allowed, _ := limiter.Allow("127.0.0.1|user@example.com", now.Add(10*time.Second)); !allowed {
		t.Fatal("second request should be allowed")
	}
	if allowed, retryAfter := limiter.Allow("127.0.0.1|user@example.com", now.Add(20*time.Second)); allowed {
		t.Fatal("third request should be rate-limited")
	} else if retryAfter <= 0 {
		t.Fatal("retryAfter should be positive when rate-limited")
	}
	if allowed, _ := limiter.Allow("127.0.0.1|user@example.com", now.Add(2*time.Minute)); !allowed {
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

func TestNormalizeRateLimitPath(t *testing.T) {
	tests := map[string]string{
		"/api/login":             "/login",
		"/api/auth/reset":        "/auth/reset",
		"/login":                 "/login",
		"/api":                   "/",
		"":                       "/",
		"   /api/auth/forgot   ": "/auth/forgot",
	}

	for input, want := range tests {
		if got := normalizeRateLimitPath(input); got != want {
			t.Fatalf("normalizeRateLimitPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAPIRateLimiterMiddleware(t *testing.T) {
	limiter := NewAPIRateLimiter()
	limiter.now = func() time.Time { return time.Unix(0, 0) }
	limiter.routePolicies["POST /login"] = RateLimitPolicy{Limit: 1, Window: time.Minute}

	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	first := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	first.RemoteAddr = "203.0.113.5:1234"
	firstRecorder := httptest.NewRecorder()
	handler.ServeHTTP(firstRecorder, first)
	if firstRecorder.Code != http.StatusOK {
		t.Fatalf("first request code = %d, want %d", firstRecorder.Code, http.StatusOK)
	}

	second := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	second.RemoteAddr = "203.0.113.5:5678"
	secondRecorder := httptest.NewRecorder()
	handler.ServeHTTP(secondRecorder, second)
	if secondRecorder.Code != http.StatusTooManyRequests {
		t.Fatalf("second request code = %d, want %d", secondRecorder.Code, http.StatusTooManyRequests)
	}
	if secondRecorder.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After header should be set on rate-limited responses")
	}
}
