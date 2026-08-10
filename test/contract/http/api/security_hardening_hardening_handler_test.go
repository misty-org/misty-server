package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

// These lock in the protections that stand between an abusive caller and a
// provider bill. Each one failed before the hardening pass.

func hardeningHandler(limiter *APIRateLimiter) http.Handler {
	return limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

func TestForwardedHeaderCannotForgeARateLimitKey(t *testing.T) {
	t.Setenv("TRUST_PROXY_HEADERS", "true")
	ResetTrustedProxyCacheForTest()
	t.Cleanup(ResetTrustedProxyCacheForTest)

	handler := hardeningHandler(NewAPIRateLimiter())
	allowed := 0
	for i := 0; i < 200; i++ {
		request := httptest.NewRequest(http.MethodPost, "/login", nil)
		request.RemoteAddr = "10.0.0.9:1234"
		// nginx's $proxy_add_x_forwarded_for appends the real peer, so the
		// attacker's value sits to the left and must be ignored.
		request.Header.Set("X-Forwarded-For", fmt.Sprintf("1.2.3.%d, 203.0.113.7", i%256))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code == http.StatusOK {
			allowed++
		}
	}
	if allowed > 20 {
		t.Fatalf("forged X-Forwarded-For allowed %d requests past a 20/min limit", allowed)
	}
}

func TestForwardedHeaderIgnoredFromUntrustedPeer(t *testing.T) {
	t.Setenv("TRUST_PROXY_HEADERS", "true")
	ResetTrustedProxyCacheForTest()
	t.Cleanup(ResetTrustedProxyCacheForTest)

	// A direct connection from the public internet may not claim a chain, even
	// when proxy trust is switched on.
	request := httptest.NewRequest(http.MethodPost, "/login", nil)
	request.RemoteAddr = "198.51.100.4:9999"
	request.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := TestingClientIPFromRequest(request); got != "198.51.100.4" {
		t.Fatalf("clientIPFromRequest() = %q, want the untrusted peer's own address", got)
	}
}

func TestForwardedChainSkipsTrustedHops(t *testing.T) {
	t.Setenv("TRUST_PROXY_HEADERS", "true")
	ResetTrustedProxyCacheForTest()
	t.Cleanup(ResetTrustedProxyCacheForTest)

	request := httptest.NewRequest(http.MethodPost, "/login", nil)
	request.RemoteAddr = "10.0.0.9:1234"
	request.Header.Set("X-Forwarded-For", "1.2.3.4, 203.0.113.7, 10.0.0.9")
	if got := TestingClientIPFromRequest(request); got != "203.0.113.7" {
		t.Fatalf("clientIPFromRequest() = %q, want the right-most untrusted hop", got)
	}
}

func TestConfiguredProxyCIDRIsTrusted(t *testing.T) {
	t.Setenv("TRUST_PROXY_HEADERS", "true")
	t.Setenv("TRUSTED_PROXY_CIDRS", "198.51.100.0/24")
	ResetTrustedProxyCacheForTest()
	t.Cleanup(ResetTrustedProxyCacheForTest)

	request := httptest.NewRequest(http.MethodPost, "/login", nil)
	request.RemoteAddr = "198.51.100.4:9999"
	request.Header.Set("X-Forwarded-For", "203.0.113.7")
	if got := TestingClientIPFromRequest(request); got != "203.0.113.7" {
		t.Fatalf("clientIPFromRequest() = %q, want the client behind a configured proxy", got)
	}
}

func TestLimiterStateStaysBounded(t *testing.T) {
	limiter := NewAPIRateLimiter()
	handler := hardeningHandler(limiter)
	for i := 0; i < 5000; i++ {
		request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/invented-%d", i), nil)
		request.RemoteAddr = "203.0.113.7:1234"
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	limiter.TestingMu.Lock()
	tracked := len(limiter.TestingLimiters)
	limiter.TestingMu.Unlock()
	if tracked > TestingMaxTrackedRoutes+1 {
		t.Fatalf("retained %d limiters, want at most %d", tracked, TestingMaxTrackedRoutes+1)
	}
}

func TestLimiterCallerStateStaysBounded(t *testing.T) {
	limiter := NewSlidingWindowLimiter(5, time.Minute)
	limiter.TestingMaxKeys = 100
	now := time.Now()
	for i := 0; i < 5000; i++ {
		limiter.Allow(fmt.Sprintf("caller-%d", i), now)
	}
	if tracked := limiter.TrackedKeys(); tracked > 100 {
		t.Fatalf("tracked %d callers, want the cap of 100 to hold", tracked)
	}
}

func TestExpiredCallersArePurgedSoLegitimateTrafficStillFlows(t *testing.T) {
	limiter := NewSlidingWindowLimiter(5, time.Minute)
	limiter.TestingMaxKeys = 50
	start := time.Now()
	for i := 0; i < 50; i++ {
		limiter.Allow(fmt.Sprintf("old-%d", i), start)
	}
	// Once the old window has elapsed those entries are dead weight; a new
	// caller must not be refused because a past spray filled the table.
	later := start.Add(2 * time.Minute)
	if allowed, _ := limiter.Allow("fresh-caller", later); !allowed {
		t.Fatal("a new caller was refused after the old window expired")
	}
}

func TestSpaceRoutesShareOneBudgetPerRouteShape(t *testing.T) {
	limiter := NewAPIRateLimiter()
	handler := hardeningHandler(limiter)

	allowed := 0
	for i := 0; i < 100; i++ {
		// A different Space each time must not mint a fresh sync budget: the
		// upstream Google/Discord cost is per call, not per Space.
		path := fmt.Sprintf("/spaces/space_%036d/calendar/sync", i)
		request := httptest.NewRequest(http.MethodPost, path, nil)
		request.RemoteAddr = "203.0.113.7:1234"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code == http.StatusOK {
			allowed++
		}
	}
	if allowed > 6 {
		t.Fatalf("calendar sync allowed %d calls across Spaces, want the 6/min budget", allowed)
	}
}

func TestProviderRoutesNormalizeToTheirPolicy(t *testing.T) {
	cases := map[string]string{
		"/api/spaces/space_abcdefabcdefabcdef/calendar/sync":                                       "/spaces/{spaceID}/calendar/sync",
		"/spaces/space_abcdefabcdefabcdef/integrations/discord/link":                               "/spaces/{spaceID}/integrations/discord/link",
		"/spaces/space_abcdefabcdefabcdef/integrations/discord/link/discordlink_x1x2x3x4x5x6/sync": "/spaces/{spaceID}/integrations/discord/link/{id}/sync",
		"/spaces/space_abcdefabcdefabcdef/integrations/notion/search":                              "/spaces/{spaceID}/integrations/notion/search",
		"/spaces/space_abcdefabcdefabcdef/integrations/discord/authorize":                          "/spaces/{spaceID}/integrations/{provider}/authorize",
		"/spaces/space_abcdefabcdefabcdef/tasks/calendar":                                          "/spaces/{spaceID}/tasks/calendar",
	}
	for input, want := range cases {
		if got := TestingNormalizeRateLimitPath(input); got != want {
			t.Fatalf("normalizeRateLimitPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestPublicBetaSensitiveRoutesHaveExplicitRateLimits(t *testing.T) {
	limiter := NewAPIRateLimiter()
	cases := []struct {
		path  string
		limit int
	}{
		{"/spaces/space_abcdefabcdefabcdef/notes/note_abcdefabcdefabcdef/collaboration-ticket", 30},
		{"/spaces/space_abcdefabcdefabcdef/drawings/drawing_abcdefabcdefabcdef/collaboration-ticket", 30},
		{"/spaces/space_abcdefabcdefabcdef/notes/note_abcdefabcdefabcdef/assets/uploads", 20},
		{"/spaces/space_abcdefabcdefabcdef/drawings/drawing_abcdefabcdefabcdef/assets/uploads", 20},
		{"/spaces/space_abcdefabcdefabcdef/notes/note_abcdefabcdefabcdef/assets/uploads/upload_abcdefabcdefabcdef/finalize", 30},
		{"/spaces/space_abcdefabcdefabcdef/drawings/drawing_abcdefabcdefabcdef/assets/uploads/upload_abcdefabcdefabcdef/finalize", 30},
	}
	for _, item := range cases {
		path := TestingNormalizeRateLimitPath(item.path)
		policy, exists := limiter.TestingRoutePolicies[http.MethodPost+" "+path]
		if !exists || policy.Limit != item.limit {
			t.Fatalf(
				"route %q normalized to %q with policy %#v, want explicit limit %d",
				item.path, path, policy, item.limit,
			)
		}
	}
}

func TestAbuseGuardBlocksSustainedAbuse(t *testing.T) {
	policy := AbusePolicy{
		TotalLimit: 5, TotalWindow: time.Minute,
		StrikesBeforeBlock: 3, StrikeWindow: time.Minute,
		BaseBlock: time.Minute, MaxBlock: 10 * time.Minute,
	}
	guard := NewAbuseGuard(policy)
	handler := guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	statuses := map[int]int{}
	for i := 0; i < 30; i++ {
		request := httptest.NewRequest(http.MethodGet, "/spaces", nil)
		request.RemoteAddr = "203.0.113.7:1234"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		statuses[recorder.Code]++
	}
	if statuses[http.StatusOK] > policy.TotalLimit {
		t.Fatalf("total ceiling let %d requests through, want %d", statuses[http.StatusOK], policy.TotalLimit)
	}
	if blocked, _ := guard.Blocked("203.0.113.7"); !blocked {
		t.Fatal("sustained rejections should have escalated to a block")
	}
}

func TestAbuseGuardDoesNotBlockAnIsolatedBurst(t *testing.T) {
	policy := DefaultAbusePolicy()
	guard := NewAbuseGuard(policy)
	// A handful of rejections is a client misbehaving briefly, not an attack.
	for i := 0; i < policy.StrikesBeforeBlock-1; i++ {
		guard.RecordRejection("203.0.113.7")
	}
	if blocked, _ := guard.Blocked("203.0.113.7"); blocked {
		t.Fatal("an isolated burst must not be blocked")
	}
}
