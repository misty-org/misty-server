package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/misty-org/misty-server/internal/platform/httpapi"
)

func TestSlidingWindowLimiter(t *testing.T) {
	limiter := NewSlidingWindowLimiter(2, time.Minute)
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

func TestClientIPUsesForwardedIPWhenTrusted(t *testing.T) {
	t.Setenv("TRUST_PROXY_HEADERS", "true")

	req := httptest.NewRequest("POST", "/login", nil)
	req.RemoteAddr = "10.0.0.8:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.8")

	if ip := ClientIPFromRequest(req); ip != "203.0.113.5" {
		t.Fatalf("ClientIPFromRequest() = %q", ip)
	}
}

func TestClientIPIgnoresForwardedHeadersByDefault(t *testing.T) {
	req := httptest.NewRequest("POST", "/auth/forgot", nil)
	req.RemoteAddr = "10.0.0.8:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	req.Header.Set("X-Real-IP", "203.0.113.6")

	if got := ClientIPFromRequest(req); got != "10.0.0.8" {
		t.Fatalf("clientIPFromRequest() = %q, want remote address", got)
	}
}

func TestNormalizeRateLimitPath(t *testing.T) {
	tests := map[string]string{
		"/v1/login":             "/login",
		"/v1/auth/reset":        "/auth/reset",
		"/login":                "/login",
		"/v1":                   "/",
		"":                      "/",
		"   /v1/auth/forgot   ": "/auth/forgot",
	}

	for input, want := range tests {
		if got := NormalizeRateLimitPath(input); got != want {
			t.Fatalf("normalizeRateLimitPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAPIRateLimiterMiddleware(t *testing.T) {
	limiter := NewAPIRateLimiter()
	limiter.Now = func() time.Time { return time.Unix(0, 0) }
	limiter.RoutePolicies["POST /login"] = RateLimitPolicy{Limit: 1, Window: time.Minute}

	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	first := httptest.NewRequest(http.MethodPost, "/v1/login", nil)
	first.RemoteAddr = "203.0.113.5:1234"
	firstRecorder := httptest.NewRecorder()
	handler.ServeHTTP(firstRecorder, first)
	if firstRecorder.Code != http.StatusOK {
		t.Fatalf("first request code = %d, want %d", firstRecorder.Code, http.StatusOK)
	}

	second := httptest.NewRequest(http.MethodPost, "/v1/login", nil)
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

func TestAPIRateLimiterSharesOneBudgetAcrossSpaces(t *testing.T) {
	limiter := NewAPIRateLimiter()
	limiter.Now = func() time.Time { return time.Unix(0, 0) }
	limiter.RoutePolicies["POST /spaces/{spaceID}/leave"] = RateLimitPolicy{Limit: 1, Window: time.Minute}
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	for index, spaceID := range []string{"first", "second"} {
		request := httptest.NewRequest(http.MethodPost, "/v1/spaces/"+spaceID+"/leave", nil)
		request.RemoteAddr = "203.0.113.10:1234"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		want := http.StatusOK
		if index == 1 {
			want = http.StatusTooManyRequests
		}
		if recorder.Code != want {
			t.Fatalf("request %d status=%d, want %d", index, recorder.Code, want)
		}
	}
}
