package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
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
	t.Setenv("TRUST_PROXY_HEADERS", "true")

	req := httptest.NewRequest("POST", "/auth/forgot", nil)
	req.RemoteAddr = "10.0.0.8:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.8")

	key := TestingForgotPasswordRateLimitKey(req, "User@example.com")
	if key != "203.0.113.5|user@example.com" {
		t.Fatalf("forgotPasswordRateLimitKey() = %q", key)
	}
}

func TestClientIPIgnoresForwardedHeadersByDefault(t *testing.T) {
	req := httptest.NewRequest("POST", "/auth/forgot", nil)
	req.RemoteAddr = "10.0.0.8:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	req.Header.Set("X-Real-IP", "203.0.113.6")

	if got := TestingClientIPFromRequest(req); got != "10.0.0.8" {
		t.Fatalf("clientIPFromRequest() = %q, want remote address", got)
	}
}

func TestNormalizeRateLimitPath(t *testing.T) {
	tests := map[string]string{
		"/api/login":             "/login",
		"/v1/login":              "/login",
		"/api/auth/reset":        "/auth/reset",
		"/v1/auth/reset":         "/auth/reset",
		"/login":                 "/login",
		"/api":                   "/",
		"/v1":                    "/",
		"":                       "/",
		"   /api/auth/forgot   ": "/auth/forgot",
	}

	for input, want := range tests {
		if got := TestingNormalizeRateLimitPath(input); got != want {
			t.Fatalf("normalizeRateLimitPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAIRequestGuardLimitsConcurrencyAndRateWithoutQueueing(t *testing.T) {
	guard := NewAIRequestGuard()
	guard.TestingNow = func() time.Time { return time.Unix(0, 0) }
	guard.TestingPerMinute = NewSlidingWindowLimiter(2, time.Minute)
	guard.TestingPerHour = NewSlidingWindowLimiter(10, time.Hour)

	release, _, allowed := guard.AcquireProviderCall("user")
	if !allowed {
		t.Fatal("first provider call should be allowed")
	}
	if _, _, allowed := guard.AcquireProviderCall("user"); allowed {
		t.Fatal("concurrent provider call should be rejected, not queued")
	}
	release()

	release, _, allowed = guard.AcquireProviderCall("user")
	if !allowed {
		t.Fatal("second sequential provider call should be allowed")
	}
	release()
	if _, retryAfter, allowed := guard.AcquireProviderCall("user"); allowed || retryAfter <= 0 {
		t.Fatalf("third provider call allowed=%v retryAfter=%v, want rate limit", allowed, retryAfter)
	}
}

func TestAPIRateLimiterMiddleware(t *testing.T) {
	limiter := NewAPIRateLimiter()
	limiter.TestingNow = func() time.Time { return time.Unix(0, 0) }
	limiter.TestingRoutePolicies["POST /login"] = RateLimitPolicy{Limit: 1, Window: time.Minute}

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

func TestAPIRateLimiterReservesMCPForAuthenticatedRunLimit(t *testing.T) {
	limiter := NewAPIRateLimiter()
	policy, ok := limiter.TestingRoutePolicies["POST /mcp"]
	if !ok {
		t.Fatal("expected an explicit public MCP rate-limit policy")
	}
	if policy.Limit < 120 {
		t.Fatalf("MCP public limit = %d, want shared-runtime headroom", policy.Limit)
	}

	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for requestNumber := 0; requestNumber < 31; requestNumber++ {
		request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		request.RemoteAddr = "203.0.113.8:44200"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("MCP request %d status = %d, want %d", requestNumber+1, response.Code, http.StatusNoContent)
		}
	}
}

func TestAPIRateLimiterSharesLibraryReauthenticationBudgetAcrossSpaces(t *testing.T) {
	limiter := NewAPIRateLimiter()
	limiter.TestingNow = func() time.Time { return time.Unix(0, 0) }
	limiter.TestingRoutePolicies["POST /spaces/{spaceID}/library/reauthenticate"] = RateLimitPolicy{Limit: 1, Window: time.Minute}
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	for index, spaceID := range []string{"first", "second"} {
		request := httptest.NewRequest(http.MethodPost, "/api/spaces/"+spaceID+"/library/reauthenticate", nil)
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
