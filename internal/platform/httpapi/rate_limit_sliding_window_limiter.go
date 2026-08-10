package api

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

type SlidingWindowLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	history map[string][]time.Time
	// maxKeys bounds the tracked callers. Without it, a caller that varies its
	// key (a spoofed address, a sprayed path) grows this map until the process
	// is killed — the limiter becomes the denial of service.
	TestingMaxKeys int
}

type ForgotPasswordRateLimiter struct {
	*SlidingWindowLimiter
}

type RateLimitPolicy struct {
	Limit  int
	Window time.Duration
}

type APIRateLimiter struct {
	TestingMu            sync.Mutex
	TestingNow           func() time.Time
	defaultGET           RateLimitPolicy
	defaultWrite         RateLimitPolicy
	TestingRoutePolicies map[string]RateLimitPolicy
	TestingLimiters      map[string]*SlidingWindowLimiter
	// abuse escalates repeated rejections into a temporary block.
	abuse *AbuseGuard
}

// WithAbuseGuard wires rejections into the shared abuse guard.
func (l *APIRateLimiter) WithAbuseGuard(guard *AbuseGuard) *APIRateLimiter {
	l.abuse = guard
	return l
}

func NewSlidingWindowLimiter(limit int, window time.Duration) *SlidingWindowLimiter {
	if limit <= 0 {
		limit = 5
	}
	if window <= 0 {
		window = time.Minute
	}

	return &SlidingWindowLimiter{
		limit:          limit,
		window:         window,
		history:        make(map[string][]time.Time),
		TestingMaxKeys: defaultLimiterMaxKeys,
	}
}

// defaultLimiterMaxKeys caps distinct callers tracked per limiter. Well past
// any realistic concurrent client count, small enough to bound memory.
const defaultLimiterMaxKeys = 20000

// Keys are client addresses, so this tracks distinct callers per route.

func NewForgotPasswordRateLimiter(limit int, window time.Duration) *ForgotPasswordRateLimiter {
	return &ForgotPasswordRateLimiter{
		SlidingWindowLimiter: NewSlidingWindowLimiter(limit, window),
	}
}

func NewAPIRateLimiter() *APIRateLimiter {
	return &APIRateLimiter{
		TestingNow:   time.Now,
		defaultGET:   RateLimitPolicy{Limit: 120, Window: time.Minute},
		defaultWrite: RateLimitPolicy{Limit: 30, Window: time.Minute},
		TestingRoutePolicies: map[string]RateLimitPolicy{
			"POST /register":                                            {Limit: 10, Window: time.Minute},
			"POST /login":                                               {Limit: 20, Window: time.Minute},
			"POST /waitlist":                                            {Limit: 10, Window: time.Minute},
			"POST /auth/forgot":                                         {Limit: 8, Window: time.Minute},
			"GET /auth/reset/start":                                     {Limit: 20, Window: time.Minute},
			"GET /auth/reset/validate":                                  {Limit: 20, Window: time.Minute},
			"POST /auth/reset":                                          {Limit: 10, Window: time.Minute},
			"POST /auth/handoff":                                        {Limit: 20, Window: time.Minute},
			"GET /auth/handoff/start":                                   {Limit: 20, Window: time.Minute},
			"POST /me/export":                                           {Limit: 3, Window: time.Hour},
			"POST /me/deletion":                                         {Limit: 3, Window: time.Hour},
			"POST /account/deletion/status":                             {Limit: 20, Window: time.Minute},
			"POST /billing/trial/start":                                 {Limit: 10, Window: time.Minute},
			"POST /billing/checkout-session":                            {Limit: 10, Window: time.Minute},
			"POST /billing/credit-checkout-session":                     {Limit: 10, Window: time.Minute},
			"POST /billing/portal-session":                              {Limit: 20, Window: time.Minute},
			"POST /stripe/webhook":                                      {Limit: 120, Window: time.Minute},
			"POST /ai/complete":                                         {Limit: 12, Window: time.Minute},
			"POST /ai/media-search/chunks":                              {Limit: 60, Window: time.Minute},
			"POST /ai/media-search/search":                              {Limit: 60, Window: time.Minute},
			"POST /spaces/{spaceID}/library/reauthenticate":             {Limit: 5, Window: time.Minute},
			"POST /spaces/{spaceID}/notes/{id}/collaboration-ticket":    {Limit: 30, Window: time.Minute},
			"POST /spaces/{spaceID}/drawings/{id}/collaboration-ticket": {Limit: 30, Window: time.Minute},
			"POST /spaces/{spaceID}/notes/{id}/assets/uploads":          {Limit: 20, Window: time.Minute},
			"POST /spaces/{spaceID}/drawings/{id}/assets/uploads":       {Limit: 20, Window: time.Minute},
			"POST /spaces/{spaceID}/notes/{id}/assets/uploads/{id}/finalize": {
				Limit: 30, Window: time.Minute,
			},
			"POST /spaces/{spaceID}/drawings/{id}/assets/uploads/{id}/finalize": {
				Limit: 30, Window: time.Minute,
			},

			// Provider fan-out: each of these makes an upstream call on Misty's own
			// credentials, so abuse burns third-party quota and can get the app
			// rate limited or banned rather than merely costing us CPU.
			"POST /spaces/{spaceID}/integrations/discord/link":               {Limit: 5, Window: time.Minute},
			"POST /spaces/{spaceID}/integrations/discord/link/{id}/sync":     {Limit: 10, Window: time.Minute},
			"POST /spaces/{spaceID}/integrations/discord/link/{id}/publish":  {Limit: 20, Window: time.Minute},
			"POST /spaces/{spaceID}/integrations/discord/links":              {Limit: 5, Window: time.Minute},
			"POST /spaces/{spaceID}/integrations/discord/links/{id}/sync":    {Limit: 10, Window: time.Minute},
			"POST /spaces/{spaceID}/integrations/discord/links/{id}/publish": {Limit: 20, Window: time.Minute},
			"GET /spaces/{spaceID}/integrations/notion/sources":              {Limit: 10, Window: time.Minute},
			"GET /spaces/{spaceID}/integrations/notion/search":               {Limit: 20, Window: time.Minute},
			"POST /spaces/{spaceID}/integrations/notion/pages":               {Limit: 20, Window: time.Minute},
			"POST /spaces/{spaceID}/calendar/sync":                           {Limit: 6, Window: time.Minute},

			// OAuth start is cheap for us but creates state rows and drives users
			// at a third-party consent screen.
			"POST /spaces/{spaceID}/integrations/{provider}/authorize": {Limit: 10, Window: time.Minute},
		},
		TestingLimiters: make(map[string]*SlidingWindowLimiter),
	}
}

func (l *SlidingWindowLimiter) Allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := now.Add(-l.window)
	requests := l.history[key][:0]
	for _, ts := range l.history[key] {
		if ts.After(cutoff) {
			requests = append(requests, ts)
		}
	}

	if len(requests) == 0 && len(l.history) >= l.TestingMaxKeys {
		// A new caller arriving at capacity: drop entries whose window has
		// fully elapsed, which is the common case under a spray.
		l.purgeExpired(cutoff)
	}
	if len(requests) == 0 && len(l.history) >= l.TestingMaxKeys {
		// Still full of live entries, so this is sustained abuse rather than
		// churn. Refuse rather than grow without bound.
		return false, l.window
	}

	if len(requests) >= l.limit {
		l.history[key] = requests
		retryAfter := requests[0].Add(l.window).Sub(now)
		if retryAfter < 0 {
			retryAfter = 0
		}
		return false, retryAfter
	}

	l.history[key] = append(requests, now)
	return true, 0
}

// purgeExpired drops callers with no activity inside the current window.
// The caller must hold the mutex.
func (l *SlidingWindowLimiter) purgeExpired(cutoff time.Time) {
	for key, timestamps := range l.history {
		live := false
		for _, timestamp := range timestamps {
			if timestamp.After(cutoff) {
				live = true
				break
			}
		}
		if !live {
			delete(l.history, key)
		}
	}
}

// TrackedKeys reports how many callers this limiter is holding state for.
func (l *SlidingWindowLimiter) TrackedKeys() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.history)
}

func (l *APIRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		path := TestingNormalizeRateLimitPath(r.URL.Path)
		policy := l.policyFor(r.Method, path)
		limiter := l.limiterFor(r.Method, path, policy)
		// Cost-bearing routes are charged to the account, so spreading a
		// credential across many addresses buys no extra budget. Everything
		// else stays keyed on the address, which is the only identity an
		// unauthenticated caller has.
		key := TestingClientIPFromRequest(r)
		if costBearingRoutes[path] {
			key = TestingRateLimitIdentity(r)
		}
		allowed, retryAfter := limiter.Allow(key, l.TestingNow())
		if !allowed {
			if l.abuse != nil {
				l.abuse.RecordRejection(key)
			}
			w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (l *APIRateLimiter) policyFor(method, path string) RateLimitPolicy {
	if policy, ok := l.TestingRoutePolicies[method+" "+path]; ok {
		return policy
	}
	if method == http.MethodGet {
		return l.defaultGET
	}
	return l.defaultWrite
}

// maxTrackedRoutes bounds the limiters map. Real deployments register far
// fewer route shapes; anything beyond this is a caller inventing paths, and
// those share one overflow bucket instead of allocating unboundedly.
const TestingMaxTrackedRoutes = 512
